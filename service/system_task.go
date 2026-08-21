package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	// systemTaskRunnerIdleInterval is the fallback poll interval used to pick up
	// tasks created on other nodes and mark expired leases failed.
	systemTaskRunnerIdleInterval = 15 * time.Second
	systemTaskLockTTL            = 60 * time.Second
	logCleanupBatchSize          = 1000
	logCleanupBatchInterval      = time.Second
	logCleanupOverloadInterval   = 10 * time.Second
	logCleanupQueryTimeout       = 10 * time.Second
	logCleanupStateInterval      = 10 * time.Second
	logCleanupScheduleInterval   = 24 * time.Hour

	// systemTaskSchedulerInterval throttles how often the scheduler/stale-lock
	// pass runs, independent of how often the runner wakes to claim tasks.
	systemTaskSchedulerInterval = 15 * time.Second
	systemTaskStaleLockInterval = 30 * time.Second
)

// SystemTaskHandler executes a claimed task of a specific type. Run owns the
// task lifecycle from claim to terminal state: it MUST call
// model.FinishSystemTask (succeeded/failed) before returning and MUST honor
// ctx cancellation, which the runner triggers if the per-type lock is lost.
type SystemTaskHandler interface {
	Type() string
	Run(ctx context.Context, task *model.SystemTask, runnerID string)
}

// ScheduledSystemTaskHandler is a SystemTaskHandler that the scheduler also
// creates periodically when enabled and the configured interval has elapsed
// since the last run.
type ScheduledSystemTaskHandler interface {
	SystemTaskHandler
	Enabled() bool
	Interval() time.Duration
	NewPayload() any
}

var (
	systemTaskHandlersMu sync.RWMutex
	systemTaskHandlers   = map[string]SystemTaskHandler{}
)

// RegisterSystemTaskHandler registers a handler keyed by its Type(). It must be
// called before StartSystemTaskRunner (or any time, since the runner snapshots
// the registry every pass). Re-registering a type replaces the previous handler.
func RegisterSystemTaskHandler(h SystemTaskHandler) {
	if h == nil {
		return
	}
	systemTaskHandlersMu.Lock()
	defer systemTaskHandlersMu.Unlock()
	systemTaskHandlers[h.Type()] = h
}

func registeredSystemTaskHandlers() []SystemTaskHandler {
	systemTaskHandlersMu.RLock()
	defer systemTaskHandlersMu.RUnlock()
	handlers := make([]SystemTaskHandler, 0, len(systemTaskHandlers))
	for _, h := range systemTaskHandlers {
		handlers = append(handlers, h)
	}
	return handlers
}

// logCleanupHandler executes both manual and scheduled low-impact log cleanup tasks.
type logCleanupHandler struct{}

// Type returns the persistent system task type shared by manual and scheduled cleanup.
func (logCleanupHandler) Type() string { return model.SystemTaskTypeLogCleanup }

// Enabled reports whether automatic relational-database log retention is active.
func (logCleanupHandler) Enabled() bool {
	return !common.UsingLogDatabase(common.DatabaseTypeClickHouse) &&
		common.GetDatabaseLogRetentionDays() > 0
}

// Interval returns the fixed cadence for automatic database log retention.
func (logCleanupHandler) Interval() time.Duration { return logCleanupScheduleInterval }

// NewPayload freezes the current retention cutoff for one scheduled run.
func (logCleanupHandler) NewPayload() any {
	retentionDays := common.GetDatabaseLogRetentionDays()
	targetTimestamp := int64(0)
	if retentionDays > 0 {
		targetTimestamp = common.GetTimestamp() - int64(retentionDays)*24*60*60
	}
	return LogCleanupPayload{
		TargetTimestamp: targetTimestamp,
		BatchSize:       logCleanupBatchSize,
	}
}

// Run executes one claimed cleanup task until completion, cancellation, or failure.
func (logCleanupHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	runLogCleanupTask(ctx, task, runnerID)
}

func init() {
	RegisterSystemTaskHandler(logCleanupHandler{})
}

type LogCleanupPayload struct {
	TargetTimestamp int64 `json:"target_timestamp"`
	BatchSize       int   `json:"batch_size"`
}

type LogCleanupState struct {
	Total     int64 `json:"total,omitempty"`
	Processed int64 `json:"processed"`
	Progress  *int  `json:"progress,omitempty"`
	Remaining int64 `json:"remaining,omitempty"`
}

type LogCleanupResult struct {
	DeletedCount int64 `json:"deleted_count"`
}

var (
	systemTaskRunnerOnce sync.Once
	// systemTaskWakeup signals the runner to check for runnable tasks
	// immediately instead of waiting for the idle poll. Buffered so a signal
	// raised while the runner is busy is not lost and is handled on the next loop.
	systemTaskWakeup = make(chan struct{}, 1)
)

// notifySystemTaskRunner wakes the runner without blocking. If a wakeup is
// already pending it is a no-op, which is fine since one pass drains all work.
func notifySystemTaskRunner() {
	select {
	case systemTaskWakeup <- struct{}{}:
	default:
	}
}

func StartSystemTaskRunner() {
	systemTaskRunnerOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		runnerID := fmt.Sprintf("%s-%s", common.NodeName, common.GetRandomString(8))
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("system task runner started: runner=%s idle_interval=%s", runnerID, systemTaskRunnerIdleInterval))

			ticker := time.NewTicker(systemTaskRunnerIdleInterval)
			defer ticker.Stop()

			var lastScheduler time.Time
			var lastStaleLockCleanup time.Time
			runPass := func() {
				// The scheduler/stale-lock pass is throttled independently of the
				// claim pass: wakeups (e.g. a manual log cleanup) should claim
				// immediately without re-running the scheduler every time.
				now := time.Now()
				if now.Sub(lastStaleLockCleanup) >= systemTaskStaleLockInterval {
					lastStaleLockCleanup = now
					if err := model.ExpireStaleSystemTaskLocks(common.GetTimestamp()); err != nil {
						logger.LogWarn(context.Background(), fmt.Sprintf("system task stale lock cleanup failed: %v", err))
					}
				}
				if now.Sub(lastScheduler) >= systemTaskSchedulerInterval {
					lastScheduler = now
					runSystemTaskScheduler()
				}
				runSystemTaskClaimPass(runnerID)
			}

			runPass()
			for {
				select {
				case <-ticker.C:
				case <-systemTaskWakeup:
				}
				runPass()
			}
		})
	})
}

func StartLogCleanupTask(targetTimestamp int64) (*model.SystemTask, error) {
	if targetTimestamp <= 0 {
		return nil, errors.New("target timestamp is required")
	}

	activeTask, err := model.GetActiveSystemTask(model.SystemTaskTypeLogCleanup)
	if err != nil {
		return nil, err
	}
	if activeTask != nil {
		return activeTask, nil
	}

	payload := LogCleanupPayload{
		TargetTimestamp: targetTimestamp,
		BatchSize:       logCleanupBatchSize,
	}
	state := LogCleanupState{}
	task, err := model.CreateSystemTask(model.SystemTaskTypeLogCleanup, payload, state)
	if err != nil {
		activeTask, activeErr := model.GetActiveSystemTask(model.SystemTaskTypeLogCleanup)
		if activeErr == nil && activeTask != nil {
			return activeTask, nil
		}
		return nil, err
	}
	notifySystemTaskRunner()
	return task, nil
}

// EnqueueSystemTask creates an on-demand task of the given type. The returned
// bool is true only when a new pending row was created; false means an active
// task of the same type already exists and was returned.
func EnqueueSystemTask(taskType string, payload any) (*model.SystemTask, bool, error) {
	activeTask, err := model.GetActiveSystemTask(taskType)
	if err != nil {
		return nil, false, err
	}
	if activeTask != nil {
		return activeTask, false, nil
	}

	task, err := model.CreateSystemTask(taskType, payload, nil)
	if err != nil {
		activeTask, activeErr := model.GetActiveSystemTask(taskType)
		if activeErr == nil && activeTask != nil {
			return activeTask, false, nil
		}
		return nil, false, err
	}
	notifySystemTaskRunner()
	return task, true, nil
}

// runSystemTaskClaimPass tries to claim one pending task per registered type
// and dispatches each claimed task in its own goroutine so a long-running
// handler (e.g. channel test) never blocks another type (e.g. log cleanup).
func runSystemTaskClaimPass(runnerID string) {
	handlers := registeredSystemTaskHandlers()
	taskTypes := make([]string, 0, len(handlers))
	for _, handler := range handlers {
		taskTypes = append(taskTypes, handler.Type())
	}
	pendingTasks, err := model.FindEarliestPendingSystemTasks(taskTypes)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("system task runner query failed: %v", err))
		return
	}
	for _, handler := range handlers {
		task := pendingTasks[handler.Type()]
		if task == nil {
			continue
		}
		claimedTask, claimed, err := model.ClaimSystemTask(task.ID, handler.Type(), runnerID, systemTaskLockUntil())
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("system task claim failed: %v", err))
			continue
		}
		if !claimed {
			continue
		}
		dispatchHandler := handler
		dispatchTask := claimedTask
		gopool.Go(func() {
			runWithLeaseHeartbeat(dispatchTask, runnerID, func(ctx context.Context) {
				dispatchHandler.Run(ctx, dispatchTask, runnerID)
			})
		})
	}
}

// runSystemTaskScheduler creates a new task row for each enabled scheduled
// handler whose interval has elapsed since its last run and that has no active
// row. The task active_key unique index deduplicates concurrent creation while
// the per-type lock guarantees only one runner executes the task.
func runSystemTaskScheduler() {
	now := common.GetTimestamp()
	handlers := registeredSystemTaskHandlers()
	scheduledHandlers := make([]ScheduledSystemTaskHandler, 0, len(handlers))
	taskTypes := make([]string, 0, len(handlers))
	for _, handler := range handlers {
		scheduled, ok := handler.(ScheduledSystemTaskHandler)
		if !ok || !scheduled.Enabled() {
			continue
		}
		scheduledHandlers = append(scheduledHandlers, scheduled)
		taskTypes = append(taskTypes, scheduled.Type())
	}
	latestTasks, err := model.GetLatestSystemTasks(taskTypes)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("system task scheduler query failed: %v", err))
		return
	}
	for _, scheduled := range scheduledHandlers {
		latest := latestTasks[scheduled.Type()]
		if latest != nil {
			if latest.Status == model.SystemTaskStatusPending || latest.Status == model.SystemTaskStatusRunning {
				continue // an active row already exists
			}
			if now-latest.UpdatedAt < int64(scheduled.Interval().Seconds()) {
				continue // not due yet
			}
		}
		if _, err := model.CreateSystemTask(scheduled.Type(), scheduled.NewPayload(), nil); err != nil {
			activeTask, activeErr := model.GetActiveSystemTask(scheduled.Type())
			if activeErr == nil && activeTask != nil {
				continue
			}
			if activeErr != nil {
				logger.LogWarn(context.Background(), fmt.Sprintf("system task scheduler active lookup failed: type=%s err=%v", scheduled.Type(), activeErr))
			}
			logger.LogWarn(context.Background(), fmt.Sprintf("system task scheduler create failed: type=%s err=%v", scheduled.Type(), err))
			continue
		}
	}
}

// runWithLeaseHeartbeat renews the per-type lock on a background ticker while
// fn runs. The TTL is a crash-detection window, not a task time limit: an
// arbitrarily long handler stays alive as long as the heartbeat succeeds.
func runWithLeaseHeartbeat(task *model.SystemTask, runnerID string, fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	interval := systemTaskLockTTL / 3
	if interval <= 0 {
		interval = systemTaskLockTTL
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := model.RenewSystemTaskLock(task.TaskID, runnerID, systemTaskLockUntil()); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	fn(ctx)
	close(done)
}

func runLogCleanupTask(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := LogCleanupPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if payload.TargetTimestamp <= 0 {
		failSystemTask(task, runnerID, errors.New("target timestamp is required"))
		return
	}
	if payload.BatchSize <= 0 || payload.BatchSize > logCleanupBatchSize {
		payload.BatchSize = logCleanupBatchSize
	}

	state := LogCleanupState{}
	if err := task.DecodeState(&state); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	// 新任务不做全量预统计；恢复旧任务时仅保留已处理数。
	state.Total = 0
	state.Remaining = 0
	state.Progress = nil
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse 保持单次同步 mutation，不进入关系型数据库的限速分批循环。
		rowsAffected, err := model.DeleteOldLogBatch(ctx, payload.TargetTimestamp, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		state.Processed += rowsAffected
		completedProgress := 100
		state.Progress = &completedProgress
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
		result := LogCleanupResult{DeletedCount: state.Processed}
		if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
			logSystemTaskLockError(ctx, task, err)
		}
		return
	}

	lastStateUpdate := time.Now()
	pausedForLoad := false

	for {
		status := common.GetSystemStatus()
		monitorConfig := common.GetPerformanceMonitorConfig()
		if shouldPauseLogCleanup(status, monitorConfig) {
			if !pausedForLoad {
				logger.LogWarn(ctx, fmt.Sprintf(
					"system task %s paused for high load: cpu=%.1f memory=%.1f disk=%.1f",
					task.TaskID,
					status.CPUUsage,
					status.MemoryUsage,
					status.DiskUsage,
				))
				pausedForLoad = true
			}
			if !waitForLogCleanup(ctx, logCleanupOverloadInterval) {
				logger.LogWarn(ctx, fmt.Sprintf("system task %s cleanup canceled: %v", task.TaskID, ctx.Err()))
				return
			}
			continue
		}
		if pausedForLoad {
			logger.LogInfo(ctx, fmt.Sprintf("system task %s resumed after load recovered", task.TaskID))
			pausedForLoad = false
		}

		// 为单批查询和删除设置上限，避免锁等待或慢 SQL 长时间占用连接。
		batchCtx, cancel := context.WithTimeout(ctx, logCleanupQueryTimeout)
		rowsAffected, err := model.DeleteOldLogBatch(batchCtx, payload.TargetTimestamp, payload.BatchSize)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				logger.LogWarn(ctx, fmt.Sprintf("system task %s cleanup canceled: %v", task.TaskID, ctx.Err()))
				return
			}
			failSystemTask(task, runnerID, err)
			return
		}
		if rowsAffected == 0 {
			break
		}

		state.Processed += rowsAffected
		if time.Since(lastStateUpdate) >= logCleanupStateInterval {
			if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
				logSystemTaskLockError(ctx, task, err)
				return
			}
			lastStateUpdate = time.Now()
		}

		// 等待发生在数据库语句之外，不持有连接、事务或行锁。
		if !waitForLogCleanup(ctx, logCleanupBatchInterval) {
			logger.LogWarn(ctx, fmt.Sprintf("system task %s cleanup canceled: %v", task.TaskID, ctx.Err()))
			return
		}
	}

	completedProgress := 100
	state.Progress = &completedProgress
	if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
		logSystemTaskLockError(ctx, task, err)
		return
	}

	result := LogCleanupResult{DeletedCount: state.Processed}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

// shouldPauseLogCleanup applies the existing performance-protection thresholds to cleanup work.
func shouldPauseLogCleanup(status common.SystemStatus, config common.PerformanceMonitorConfig) bool {
	if !config.Enabled {
		return false
	}
	if config.CPUThreshold > 0 && status.CPUUsage > float64(config.CPUThreshold) {
		return true
	}
	if config.MemoryThreshold > 0 && status.MemoryUsage > float64(config.MemoryThreshold) {
		return true
	}
	return config.DiskThreshold > 0 && status.DiskUsage > float64(config.DiskThreshold)
}

// waitForLogCleanup waits outside database work and returns false when the task is canceled.
func waitForLogCleanup(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func systemTaskLockUntil() int64 {
	return common.GetTimestamp() + int64(systemTaskLockTTL.Seconds())
}

// SystemTaskProgress is the state shape used by handlers that report percentage
// progress (channel test, model update). The frontend reads the progress field
// (0-100) to render a per-task progress indicator.
type SystemTaskProgress struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
	Progress  int `json:"progress"`
}

// NewSystemTaskProgressReporter returns a throttled progress callback bound to a
// running task. Handlers call it with (processed, total) as they iterate work;
// it persists a {processed,total,progress} state at most once every ~2s, always
// emitting the first update and the final 100%.
// Lock-loss errors are ignored: the lease heartbeat cancels the handler ctx on
// loss, so progress writes are best-effort and never abort the run themselves.
// The returned func is single-goroutine only (call it from the handler loop).
func NewSystemTaskProgressReporter(task *model.SystemTask, runnerID string) func(processed, total int) {
	const minWriteInterval = 2 * time.Second
	var (
		lastWriteAt  time.Time
		lastProgress = -1
	)
	return func(processed, total int) {
		progress := 100
		if total > 0 {
			progress = processed * 100 / total
		}
		if progress < 0 {
			progress = 0
		} else if progress > 100 {
			progress = 100
		}

		if progress < 100 {
			if progress == lastProgress {
				return
			}
			if !lastWriteAt.IsZero() && time.Since(lastWriteAt) < minWriteInterval {
				return
			}
		}
		lastProgress = progress
		lastWriteAt = time.Now()

		state := SystemTaskProgress{Total: total, Processed: processed, Progress: progress}
		_ = model.UpdateSystemTaskState(task.TaskID, runnerID, state)
	}
}

func failSystemTask(task *model.SystemTask, runnerID string, err error) {
	logger.LogWarn(context.Background(), fmt.Sprintf("system task %s failed: %v", task.TaskID, err))
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusFailed, nil, err.Error()); finishErr != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("system task %s failed to save failure state: %v", task.TaskID, finishErr))
	}
}

func logSystemTaskLockError(ctx context.Context, task *model.SystemTask, err error) {
	if errors.Is(err, model.ErrSystemTaskLockLost) {
		logger.LogWarn(ctx, fmt.Sprintf("system task %s lock lost", task.TaskID))
		return
	}
	logger.LogWarn(ctx, fmt.Sprintf("system task %s update failed: %v", task.TaskID, err))
}
