package middleware

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPerformanceStatusUsesEffectiveResourceValues(t *testing.T) {
	config := common.PerformanceMonitorConfig{
		Enabled:         true,
		CPUThreshold:    90,
		MemoryThreshold: 90,
		DiskThreshold:   95,
		ResourceScope:   common.ResourceScopeContainer,
	}

	assert.Nil(t, checkPerformanceStatus(common.SystemStatus{
		CPUUsage:    20,
		MemoryUsage: 30,
		DiskUsage:   40,
	}, config), "低容器占用不应被宿主机压力误拦截")

	cpuErr := checkPerformanceStatus(common.SystemStatus{CPUUsage: 95}, config)
	require.NotNil(t, cpuErr)
	assert.Equal(t, types.ErrorCode("system_cpu_overloaded"), cpuErr.GetErrorCode())
	assert.Equal(t, http.StatusServiceUnavailable, cpuErr.StatusCode)

	memoryErr := checkPerformanceStatus(common.SystemStatus{MemoryUsage: 95}, config)
	require.NotNil(t, memoryErr)
	assert.Equal(t, types.ErrorCode("system_memory_overloaded"), memoryErr.GetErrorCode())

	diskErr := checkPerformanceStatus(common.SystemStatus{DiskUsage: 99}, config)
	require.NotNil(t, diskErr)
	assert.Equal(t, types.ErrorCode("system_disk_overloaded"), diskErr.GetErrorCode())
}

func TestCheckPerformanceStatusDisabledOnlyDisplaysMetrics(t *testing.T) {
	config := common.PerformanceMonitorConfig{
		Enabled:         false,
		CPUThreshold:    1,
		MemoryThreshold: 1,
		DiskThreshold:   1,
	}
	assert.Nil(t, checkPerformanceStatus(common.SystemStatus{
		CPUUsage:    100,
		MemoryUsage: 100,
		DiskUsage:   100,
	}, config))
}
