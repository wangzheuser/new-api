package common

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

// DiskSpaceInfo 磁盘空间信息
type DiskSpaceInfo struct {
	// 总空间（字节）
	Total uint64 `json:"total"`
	// 可用空间（字节）
	Free uint64 `json:"free"`
	// 已用空间（字节）
	Used uint64 `json:"used"`
	// 使用百分比
	UsedPercent float64 `json:"used_percent"`
}

// ResourceStats 汇总宿主机、容器和 Go Runtime 资源指标。
type ResourceStats struct {
	ConfiguredScope              string   `json:"configured_scope"`
	EffectiveCPUScope            string   `json:"effective_cpu_scope"`
	EffectiveMemoryScope         string   `json:"effective_memory_scope"`
	HostCPUUsagePercent          *float64 `json:"host_cpu_usage_percent,omitempty"`
	HostMemoryUsagePercent       *float64 `json:"host_memory_usage_percent,omitempty"`
	HostCPUStealPercent          *float64 `json:"host_cpu_steal_percent,omitempty"`
	HostCPUPSISomeAvg60          *float64 `json:"host_cpu_psi_some_avg60,omitempty"`
	ContainerCPUUsagePercent     *float64 `json:"container_cpu_usage_percent,omitempty"`
	ContainerMemoryUsagePercent  *float64 `json:"container_memory_usage_percent,omitempty"`
	ContainerCPULimitCores       *float64 `json:"container_cpu_limit_cores,omitempty"`
	ContainerMemoryLimitBytes    *uint64  `json:"container_memory_limit_bytes,omitempty"`
	ContainerCPUThrottledPercent *float64 `json:"container_cpu_throttled_percent,omitempty"`
	GOMAXPROCS                   int      `json:"gomaxprocs"`
}

// SystemStatus 系统状态信息
type SystemStatus struct {
	CPUUsage      float64
	MemoryUsage   float64
	DiskUsage     float64
	ResourceStats ResourceStats
}

type systemResourceCollector struct {
	cgroupSampler *cgroupSampler
	hostStatMu    sync.Mutex
	previousHost  *hostCPUCounters
}

var latestSystemStatus atomic.Value
var defaultResourceCollector = &systemResourceCollector{
	cgroupSampler: newCgroupSampler("/sys/fs/cgroup", "/proc/self/cgroup"),
}

func init() {
	latestSystemStatus.Store(SystemStatus{
		ResourceStats: ResourceStats{
			ConfiguredScope:      ResourceScopeHost,
			EffectiveCPUScope:    ResourceScopeHost,
			EffectiveMemoryScope: ResourceScopeHost,
			GOMAXPROCS:           runtime.GOMAXPROCS(0),
		},
	})
}

// StartSystemMonitor 启动持续资源采集，供性能保护和管理界面使用。
func StartSystemMonitor() {
	go func() {
		for {
			updateSystemStatus()
			time.Sleep(5 * time.Second)
		}
	}()
}

// updateSystemStatus 刷新原子化系统状态快照。
func updateSystemStatus() {
	latestSystemStatus.Store(defaultResourceCollector.collect(GetPerformanceMonitorConfig()))
}

// collect 采集宿主机与 cgroup 指标，并选择配置的保护口径。
func (collector *systemResourceCollector) collect(config PerformanceMonitorConfig) SystemStatus {
	configuredScope := config.ResourceScope
	if configuredScope != ResourceScopeContainer {
		configuredScope = ResourceScopeHost
	}
	resourceStats := ResourceStats{
		ConfiguredScope:      configuredScope,
		EffectiveCPUScope:    ResourceScopeHost,
		EffectiveMemoryScope: ResourceScopeHost,
		GOMAXPROCS:           runtime.GOMAXPROCS(0),
	}

	percents, err := cpu.Percent(0, false)
	if err == nil && len(percents) > 0 {
		resourceStats.HostCPUUsagePercent = float64Ptr(clampPercent(percents[0]))
	}
	memInfo, err := mem.VirtualMemory()
	if err == nil {
		resourceStats.HostMemoryUsagePercent = float64Ptr(clampPercent(memInfo.UsedPercent))
	}
	resourceStats.HostCPUStealPercent = collector.sampleHostCPUSteal("/proc/stat")
	resourceStats.HostCPUPSISomeAvg60 = readCPUPSIAvg60("/proc/pressure/cpu")

	containerStats := containerResourceStats{}
	if IsRunningInContainer() {
		containerStats = collector.cgroupSampler.sample()
	}
	resourceStats.ContainerCPUUsagePercent = containerStats.CPUUsagePercent
	resourceStats.ContainerMemoryUsagePercent = containerStats.MemoryUsagePercent
	resourceStats.ContainerCPULimitCores = containerStats.CPULimitCores
	resourceStats.ContainerMemoryLimitBytes = containerStats.MemoryLimitBytes
	resourceStats.ContainerCPUThrottledPercent = containerStats.CPUThrottledPercent

	status := selectEffectiveResourceUsage(resourceStats)

	diskInfo := GetDiskSpaceInfo()
	if diskInfo.Total > 0 {
		status.DiskUsage = diskInfo.UsedPercent
	}
	return status
}

// selectEffectiveResourceUsage 分别应用 CPU 和内存口径及回退规则。
func selectEffectiveResourceUsage(resourceStats ResourceStats) SystemStatus {
	status := SystemStatus{ResourceStats: resourceStats}
	status.ResourceStats.EffectiveCPUScope = ResourceScopeHost
	status.ResourceStats.EffectiveMemoryScope = ResourceScopeHost
	if resourceStats.HostCPUUsagePercent != nil {
		status.CPUUsage = *resourceStats.HostCPUUsagePercent
	}
	if resourceStats.HostMemoryUsagePercent != nil {
		status.MemoryUsage = *resourceStats.HostMemoryUsagePercent
	}
	if resourceStats.ConfiguredScope != ResourceScopeContainer {
		return status
	}

	// CPU 和内存分别回退，避免一个 cgroup 指标缺失时丢弃另一个有效指标。
	if resourceStats.ContainerCPUUsagePercent != nil {
		status.CPUUsage = *resourceStats.ContainerCPUUsagePercent
		status.ResourceStats.EffectiveCPUScope = ResourceScopeContainer
	}
	if resourceStats.ContainerMemoryUsagePercent != nil {
		status.MemoryUsage = *resourceStats.ContainerMemoryUsagePercent
		status.ResourceStats.EffectiveMemoryScope = ResourceScopeContainer
	}
	return status
}

// GetSystemStatus 返回最新的只读资源快照。
func GetSystemStatus() SystemStatus {
	return latestSystemStatus.Load().(SystemStatus)
}

// float64Ptr 为可选浮点指标创建指针。
func float64Ptr(value float64) *float64 {
	return &value
}

// uint64Ptr 为可选无符号整数指标创建指针。
func uint64Ptr(value uint64) *uint64 {
	return &value
}

// clampPercent 将百分比指标归一化到公开的 0..100 范围。
func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
