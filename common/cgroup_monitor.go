package common

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type containerResourceStats struct {
	CPUUsagePercent     *float64
	MemoryUsagePercent  *float64
	CPULimitCores       *float64
	MemoryLimitBytes    *uint64
	CPUThrottledPercent *float64
}

type cgroupCounters struct {
	collectedAt        time.Time
	cpuUsageNS         uint64
	cpuThrottledNS     uint64
	cpuLimitCores      float64
	memoryCurrent      uint64
	memoryLimit        uint64
	cpuAvailable       bool
	throttledAvailable bool
	memoryAvailable    bool
}

type cgroupSampler struct {
	root               string
	procSelfCgroupPath string
	now                func() time.Time
	mu                 sync.Mutex
	previous           *cgroupCounters
}

// newCgroupSampler 创建用于计算 cgroup 增量指标的有状态采样器。
func newCgroupSampler(root string, procSelfCgroupPath string) *cgroupSampler {
	return &cgroupSampler{
		root:               root,
		procSelfCgroupPath: procSelfCgroupPath,
		now:                time.Now,
	}
}

// sample 读取当前 cgroup 计数器并计算增量百分比。
func (sampler *cgroupSampler) sample() containerResourceStats {
	current, err := sampler.readCounters()
	if err != nil {
		sampler.mu.Lock()
		sampler.previous = nil
		sampler.mu.Unlock()
		return containerResourceStats{}
	}
	current.collectedAt = sampler.now()

	result := containerResourceStats{}
	if current.cpuAvailable {
		result.CPULimitCores = float64Ptr(current.cpuLimitCores)
	}
	if current.memoryAvailable {
		result.MemoryLimitBytes = uint64Ptr(current.memoryLimit)
		usagePercent := float64(current.memoryCurrent) / float64(current.memoryLimit) * 100
		result.MemoryUsagePercent = float64Ptr(clampPercent(usagePercent))
	}

	sampler.mu.Lock()
	previous := sampler.previous
	sampler.previous = &current
	sampler.mu.Unlock()
	if previous == nil || !current.collectedAt.After(previous.collectedAt) {
		return result
	}
	elapsedSeconds := current.collectedAt.Sub(previous.collectedAt).Seconds()
	if current.cpuAvailable && previous.cpuAvailable && current.cpuUsageNS >= previous.cpuUsageNS {
		deltaUsageSeconds := float64(current.cpuUsageNS-previous.cpuUsageNS) / float64(time.Second)
		usagePercent := deltaUsageSeconds / elapsedSeconds / current.cpuLimitCores * 100
		result.CPUUsagePercent = float64Ptr(clampPercent(usagePercent))
	}
	if current.throttledAvailable && previous.throttledAvailable && current.cpuThrottledNS >= previous.cpuThrottledNS {
		deltaThrottledSeconds := float64(current.cpuThrottledNS-previous.cpuThrottledNS) / float64(time.Second)
		throttledPercent := deltaThrottledSeconds / elapsedSeconds / current.cpuLimitCores * 100
		result.CPUThrottledPercent = float64Ptr(clampPercent(throttledPercent))
	}
	return result
}

// readCounters 优先读取 cgroup v2，失败后回退到 v1 布局。
func (sampler *cgroupSampler) readCounters() (cgroupCounters, error) {
	if counters, err := sampler.readV2Counters(); err == nil {
		return counters, nil
	}
	return sampler.readV1Counters()
}

// readV2Counters 读取 cgroup v2 的 CPU、内存和限流计数器。
func (sampler *cgroupSampler) readV2Counters() (cgroupCounters, error) {
	dir := resolveCgroupV2Dir(sampler.root, sampler.procSelfCgroupPath)
	if _, err := os.Stat(filepath.Join(dir, "cpu.max")); err != nil {
		if _, memoryErr := os.Stat(filepath.Join(dir, "memory.current")); memoryErr != nil {
			return cgroupCounters{}, err
		}
	}

	counters := cgroupCounters{}
	if raw, err := os.ReadFile(filepath.Join(dir, "cpu.max")); err == nil {
		if limit, parseErr := parseCPUMax(string(raw), runtime.NumCPU()); parseErr == nil {
			if statRaw, statErr := os.ReadFile(filepath.Join(dir, "cpu.stat")); statErr == nil {
				stat := parseKeyValueCounters(string(statRaw))
				if usageUS, ok := stat["usage_usec"]; ok {
					if usageNS, valid := microsecondsToNanoseconds(usageUS); valid {
						counters.cpuUsageNS = usageNS
						counters.cpuLimitCores = limit
						counters.cpuAvailable = true
					}
				}
				if throttledUS, ok := stat["throttled_usec"]; ok {
					if throttledNS, valid := microsecondsToNanoseconds(throttledUS); valid {
						counters.cpuThrottledNS = throttledNS
						counters.throttledAvailable = counters.cpuAvailable
					}
				}
			}
		}
	}
	currentRaw, currentErr := os.ReadFile(filepath.Join(dir, "memory.current"))
	maxRaw, maxErr := os.ReadFile(filepath.Join(dir, "memory.max"))
	if currentErr == nil && maxErr == nil {
		if current, currentParseErr := parseUint(string(currentRaw)); currentParseErr == nil {
			if limit, limited, limitParseErr := parseMemoryLimit(string(maxRaw)); limitParseErr == nil && limited {
				counters.memoryCurrent = current
				counters.memoryLimit = limit
				counters.memoryAvailable = true
			}
		}
	}
	if !counters.cpuAvailable && !counters.memoryAvailable {
		return cgroupCounters{}, errors.New("cgroup v2 metrics unavailable")
	}
	return counters, nil
}

// readV1Counters 读取 cgroup v1 的 CPU、内存和限流计数器。
func (sampler *cgroupSampler) readV1Counters() (cgroupCounters, error) {
	paths := parseProcSelfCgroup(sampler.procSelfCgroupPath)
	cpuDir := resolveCgroupV1Dir(sampler.root, paths, []string{"cpu"}, []string{"cpu,cpuacct", "cpuacct,cpu", "cpu"})
	cpuAcctDir := resolveCgroupV1Dir(sampler.root, paths, []string{"cpuacct"}, []string{"cpu,cpuacct", "cpuacct,cpu", "cpuacct", "cpu"})
	memoryDir := resolveCgroupV1Dir(sampler.root, paths, []string{"memory"}, []string{"memory"})
	counters := cgroupCounters{}

	usageRaw, usageErr := os.ReadFile(filepath.Join(cpuAcctDir, "cpuacct.usage"))
	quotaRaw, quotaErr := os.ReadFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"))
	periodRaw, periodErr := os.ReadFile(filepath.Join(cpuDir, "cpu.cfs_period_us"))
	if usageErr == nil && quotaErr == nil && periodErr == nil {
		usage, usageParseErr := parseUint(string(usageRaw))
		limit, limitParseErr := parseCPUQuota(string(quotaRaw), string(periodRaw), runtime.NumCPU())
		if usageParseErr == nil && limitParseErr == nil {
			counters.cpuUsageNS = usage
			counters.cpuLimitCores = limit
			counters.cpuAvailable = true
			if statRaw, statErr := os.ReadFile(filepath.Join(cpuDir, "cpu.stat")); statErr == nil {
				stat := parseKeyValueCounters(string(statRaw))
				if throttled, ok := stat["throttled_time"]; ok {
					counters.cpuThrottledNS = throttled
					counters.throttledAvailable = true
				} else if throttledUS, ok := stat["throttled_usec"]; ok {
					if throttledNS, valid := microsecondsToNanoseconds(throttledUS); valid {
						counters.cpuThrottledNS = throttledNS
						counters.throttledAvailable = true
					}
				}
			}
		}
	}

	currentRaw, currentErr := os.ReadFile(filepath.Join(memoryDir, "memory.usage_in_bytes"))
	limitRaw, limitErr := os.ReadFile(filepath.Join(memoryDir, "memory.limit_in_bytes"))
	if currentErr == nil && limitErr == nil {
		current, currentParseErr := parseUint(string(currentRaw))
		limit, limited, limitParseErr := parseMemoryLimit(string(limitRaw))
		if currentParseErr == nil && limitParseErr == nil && limited {
			counters.memoryCurrent = current
			counters.memoryLimit = limit
			counters.memoryAvailable = true
		}
	}
	if !counters.cpuAvailable && !counters.memoryAvailable {
		return cgroupCounters{}, errors.New("cgroup v1 metrics unavailable")
	}
	return counters, nil
}

// parseCPUMax 将 cgroup v2 cpu.max 解析为 CPU 核数。
func parseCPUMax(raw string, logicalCPUs int) (float64, error) {
	fields := strings.Fields(raw)
	if len(fields) != 2 {
		return 0, errors.New("invalid cpu.max")
	}
	if fields[0] == "max" {
		return logicalCPUCount(logicalCPUs), nil
	}
	return parseCPUQuota(fields[0], fields[1], logicalCPUs)
}

// parseCPUQuota 将 cgroup quota 和 period 解析为 CPU 核数。
func parseCPUQuota(quotaRaw string, periodRaw string, logicalCPUs int) (float64, error) {
	quota, err := strconv.ParseInt(strings.TrimSpace(quotaRaw), 10, 64)
	if err != nil {
		return 0, err
	}
	period, err := strconv.ParseInt(strings.TrimSpace(periodRaw), 10, 64)
	if err != nil || period <= 0 {
		return 0, errors.New("invalid cpu period")
	}
	if quota < 0 {
		return logicalCPUCount(logicalCPUs), nil
	}
	if quota == 0 {
		return 0, errors.New("invalid cpu quota")
	}
	return float64(quota) / float64(period), nil
}

// parseMemoryLimit 解析有限内存上限，并识别无限制哨兵值。
func parseMemoryLimit(raw string) (uint64, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "max" {
		return 0, false, nil
	}
	limit, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, false, err
	}
	if limit == 0 || limit >= 1<<60 {
		return 0, false, nil
	}
	return limit, true, nil
}

// parseKeyValueCounters 解析空白符分隔的 cgroup stat 文件。
func parseKeyValueCounters(raw string) map[string]uint64 {
	values := make(map[string]uint64)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			values[fields[0]] = value
		}
	}
	return values
}

// parseUint 解析伪文件中的无符号十进制值。
func parseUint(raw string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
}

// microsecondsToNanoseconds 在避免无符号溢出的前提下转换 cgroup 计数器。
func microsecondsToNanoseconds(value uint64) (uint64, bool) {
	const nanosecondsPerMicrosecond = uint64(time.Microsecond)
	if value > ^uint64(0)/nanosecondsPerMicrosecond {
		return 0, false
	}
	return value * nanosecondsPerMicrosecond, true
}

// logicalCPUCount 将异常逻辑 CPU 数量转换为安全分母。
func logicalCPUCount(logicalCPUs int) float64 {
	if logicalCPUs < 1 {
		logicalCPUs = 1
	}
	return float64(logicalCPUs)
}

// resolveCgroupV2Dir 解析当前进程对应的 cgroup v2 目录。
func resolveCgroupV2Dir(root string, procSelfCgroupPath string) string {
	paths := parseProcSelfCgroup(procSelfCgroupPath)
	if relative, ok := paths[""]; ok {
		return filepath.Join(root, sanitizeCgroupPath(relative))
	}
	return root
}

// resolveCgroupV1Dir 按常见挂载布局解析 cgroup v1 控制器目录。
func resolveCgroupV1Dir(root string, paths map[string]string, controllers []string, mountNames []string) string {
	relative := ""
	for _, controller := range controllers {
		if value, ok := paths[controller]; ok {
			relative = sanitizeCgroupPath(value)
			break
		}
	}
	for _, mountName := range mountNames {
		candidate := filepath.Join(root, mountName, relative)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(root, mountNames[0], relative)
}

// parseProcSelfCgroup 将各控制器映射到当前进程的 cgroup 相对路径。
func parseProcSelfCgroup(path string) map[string]string {
	result := make(map[string]string)
	raw, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[1] == "" {
			result[""] = parts[2]
			continue
		}
		for _, controller := range strings.Split(parts[1], ",") {
			result[controller] = parts[2]
		}
	}
	return result
}

// sanitizeCgroupPath 确保伪文件读取始终位于配置根目录内。
func sanitizeCgroupPath(path string) string {
	cleaned := filepath.Clean("/" + path)
	return strings.TrimPrefix(cleaned, string(filepath.Separator))
}
