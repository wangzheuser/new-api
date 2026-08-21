package common

import "sync/atomic"

const (
	// ResourceScopeHost 使用宿主机 CPU 和内存指标执行性能保护。
	ResourceScopeHost = "host"
	// ResourceScopeContainer 优先使用 cgroup CPU 和内存指标执行性能保护。
	ResourceScopeContainer = "container"
	// MaxServerLogRetentionDays 限制服务器文件日志的最大自动保留天数。
	MaxServerLogRetentionDays = 3650
	// MaxDatabaseLogRetentionDays 限制数据库日志的最大自动保留天数。
	MaxDatabaseLogRetentionDays = 3650
)

// PerformanceMonitorConfig 性能监控配置
type PerformanceMonitorConfig struct {
	Enabled         bool
	CPUThreshold    int
	MemoryThreshold int
	DiskThreshold   int
	ResourceScope   string
}

var performanceMonitorConfig atomic.Value
var serverLogRetentionDays atomic.Int64
var databaseLogRetentionDays atomic.Int64

func init() {
	// 初始化默认配置
	performanceMonitorConfig.Store(PerformanceMonitorConfig{
		Enabled:         true,
		CPUThreshold:    90,
		MemoryThreshold: 90,
		DiskThreshold:   90,
		ResourceScope:   ResourceScopeHost,
	})
}

// GetPerformanceMonitorConfig 获取性能监控配置
func GetPerformanceMonitorConfig() PerformanceMonitorConfig {
	return performanceMonitorConfig.Load().(PerformanceMonitorConfig)
}

// SetPerformanceMonitorConfig 设置性能监控配置
func SetPerformanceMonitorConfig(config PerformanceMonitorConfig) {
	if config.ResourceScope != ResourceScopeContainer {
		config.ResourceScope = ResourceScopeHost
	}
	performanceMonitorConfig.Store(config)
}

// GetServerLogRetentionDays 返回服务器文件日志的自动保留天数。
func GetServerLogRetentionDays() int {
	return int(serverLogRetentionDays.Load())
}

// SetServerLogRetentionDays 更新内存中的服务器文件日志自动保留天数。
func SetServerLogRetentionDays(days int) {
	if days < 0 || days > MaxServerLogRetentionDays {
		days = 0
	}
	serverLogRetentionDays.Store(int64(days))
}

// GetDatabaseLogRetentionDays 返回数据库日志的自动保留天数。
func GetDatabaseLogRetentionDays() int {
	return int(databaseLogRetentionDays.Load())
}

// SetDatabaseLogRetentionDays 更新内存中的数据库日志自动保留天数。
func SetDatabaseLogRetentionDays(days int) {
	if days < 0 || days > MaxDatabaseLogRetentionDays {
		days = 0
	}
	databaseLogRetentionDays.Store(int64(days))
}
