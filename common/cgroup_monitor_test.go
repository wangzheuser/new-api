package common

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCgroupFixture writes one pseudo-file below a test cgroup root.
func writeCgroupFixture(t *testing.T, root string, relativePath string, value string) {
	t.Helper()
	path := filepath.Join(root, relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(value), 0o600))
}

func TestParseCgroupLimits(t *testing.T) {
	t.Run("v2 finite quota", func(t *testing.T) {
		cores, err := parseCPUMax("150000 100000", 8)
		require.NoError(t, err)
		assert.InDelta(t, 1.5, cores, 0.0001)
	})
	t.Run("v2 unlimited quota", func(t *testing.T) {
		cores, err := parseCPUMax("max 100000", 8)
		require.NoError(t, err)
		assert.Equal(t, 8.0, cores)
	})
	t.Run("v1 unlimited quota", func(t *testing.T) {
		cores, err := parseCPUQuota("-1", "100000", 4)
		require.NoError(t, err)
		assert.Equal(t, 4.0, cores)
	})
	t.Run("memory finite and unlimited", func(t *testing.T) {
		limit, limited, err := parseMemoryLimit("805306368")
		require.NoError(t, err)
		assert.True(t, limited)
		assert.EqualValues(t, 805306368, limit)

		_, limited, err = parseMemoryLimit("max")
		require.NoError(t, err)
		assert.False(t, limited)
		_, limited, err = parseMemoryLimit("9223372036854771712")
		require.NoError(t, err)
		assert.False(t, limited)
	})
	t.Run("invalid values", func(t *testing.T) {
		_, err := parseCPUMax("broken", 4)
		assert.Error(t, err)
		_, _, err = parseMemoryLimit("broken")
		assert.Error(t, err)
		_, valid := microsecondsToNanoseconds(^uint64(0))
		assert.False(t, valid)
	})
}

func TestCgroupV2SamplerCalculatesDeltasAndMemory(t *testing.T) {
	root := t.TempDir()
	procPath := filepath.Join(t.TempDir(), "self-cgroup")
	require.NoError(t, os.WriteFile(procPath, []byte("0::/\n"), 0o600))
	writeCgroupFixture(t, root, "cpu.max", "100000 100000\n")
	writeCgroupFixture(t, root, "cpu.stat", "usage_usec 1000000\nthrottled_usec 100000\n")
	writeCgroupFixture(t, root, "memory.current", "402653184\n")
	writeCgroupFixture(t, root, "memory.max", "805306368\n")

	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	sampler := newCgroupSampler(root, procPath)
	sampler.now = func() time.Time { return now }
	first := sampler.sample()
	assert.Nil(t, first.CPUUsagePercent)
	require.NotNil(t, first.CPULimitCores)
	assert.Equal(t, 1.0, *first.CPULimitCores)
	require.NotNil(t, first.MemoryUsagePercent)
	assert.Equal(t, 50.0, *first.MemoryUsagePercent)
	require.NotNil(t, first.MemoryLimitBytes)
	assert.EqualValues(t, 805306368, *first.MemoryLimitBytes)

	now = now.Add(2 * time.Second)
	writeCgroupFixture(t, root, "cpu.stat", "usage_usec 1500000\nthrottled_usec 120000\n")
	second := sampler.sample()
	require.NotNil(t, second.CPUUsagePercent)
	assert.InDelta(t, 25.0, *second.CPUUsagePercent, 0.001)
	require.NotNil(t, second.CPUThrottledPercent)
	assert.InDelta(t, 1.0, *second.CPUThrottledPercent, 0.001)

	// 计数器回绕或进程重启后，本周期回退而不是产生负数或错误的 0%。
	now = now.Add(2 * time.Second)
	writeCgroupFixture(t, root, "cpu.stat", "usage_usec 10\nthrottled_usec 1\n")
	wrapped := sampler.sample()
	assert.Nil(t, wrapped.CPUUsagePercent)
	assert.Nil(t, wrapped.CPUThrottledPercent)
}

func TestCgroupV1SamplerReadsControllerLayouts(t *testing.T) {
	root := t.TempDir()
	procPath := filepath.Join(t.TempDir(), "self-cgroup")
	require.NoError(t, os.WriteFile(procPath, []byte("2:cpu,cpuacct:/tenant\n3:memory:/tenant\n"), 0o600))
	writeCgroupFixture(t, root, "cpuacct/tenant/cpuacct.usage", "1000000000\n")
	writeCgroupFixture(t, root, "cpu/tenant/cpu.cfs_quota_us", "200000\n")
	writeCgroupFixture(t, root, "cpu/tenant/cpu.cfs_period_us", "100000\n")
	writeCgroupFixture(t, root, "cpu/tenant/cpu.stat", "throttled_time 100000000\n")
	writeCgroupFixture(t, root, "memory/tenant/memory.usage_in_bytes", "256\n")
	writeCgroupFixture(t, root, "memory/tenant/memory.limit_in_bytes", "1024\n")

	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	sampler := newCgroupSampler(root, procPath)
	sampler.now = func() time.Time { return now }
	first := sampler.sample()
	require.NotNil(t, first.CPULimitCores)
	assert.Equal(t, 2.0, *first.CPULimitCores)
	require.NotNil(t, first.MemoryUsagePercent)
	assert.Equal(t, 25.0, *first.MemoryUsagePercent)

	now = now.Add(time.Second)
	writeCgroupFixture(t, root, "cpuacct/tenant/cpuacct.usage", "2000000000\n")
	writeCgroupFixture(t, root, "cpu/tenant/cpu.stat", "throttled_time 200000000\n")
	second := sampler.sample()
	require.NotNil(t, second.CPUUsagePercent)
	assert.InDelta(t, 50.0, *second.CPUUsagePercent, 0.001)
	require.NotNil(t, second.CPUThrottledPercent)
	assert.InDelta(t, 5.0, *second.CPUThrottledPercent, 0.001)
}

func TestCgroupSamplerMissingFilesResetsDeltaBaseline(t *testing.T) {
	root := t.TempDir()
	procPath := filepath.Join(t.TempDir(), "self-cgroup")
	require.NoError(t, os.WriteFile(procPath, []byte("0::/\n"), 0o600))
	writeCgroupFixture(t, root, "cpu.max", "100000 100000\n")
	writeCgroupFixture(t, root, "cpu.stat", "usage_usec 100\n")

	now := time.Now()
	sampler := newCgroupSampler(root, procPath)
	sampler.now = func() time.Time { return now }
	assert.Nil(t, sampler.sample().CPUUsagePercent)
	require.NoError(t, os.Remove(filepath.Join(root, "cpu.stat")))
	now = now.Add(time.Second)
	assert.Nil(t, sampler.sample().CPUUsagePercent)
	writeCgroupFixture(t, root, "cpu.stat", "usage_usec 300\n")
	now = now.Add(time.Second)
	assert.Nil(t, sampler.sample().CPUUsagePercent)
}

func TestSelectEffectiveResourceUsageFallsBackIndependently(t *testing.T) {
	hostCPU, hostMemory := 95.0, 80.0
	containerCPU := 20.0
	status := selectEffectiveResourceUsage(ResourceStats{
		ConfiguredScope:             ResourceScopeContainer,
		HostCPUUsagePercent:         &hostCPU,
		HostMemoryUsagePercent:      &hostMemory,
		ContainerCPUUsagePercent:    &containerCPU,
		ContainerMemoryUsagePercent: nil,
	})
	assert.Equal(t, 20.0, status.CPUUsage)
	assert.Equal(t, 80.0, status.MemoryUsage)
	assert.Equal(t, ResourceScopeContainer, status.ResourceStats.EffectiveCPUScope)
	assert.Equal(t, ResourceScopeHost, status.ResourceStats.EffectiveMemoryScope)
}

func TestHostLinuxMetricParsers(t *testing.T) {
	counters, ok := parseHostCPUCounters("cpu 10 1 2 30 4 5 6 7 8 9\ncpu0 1 1 1 1\n")
	require.True(t, ok)
	assert.EqualValues(t, 65, counters.total)
	assert.EqualValues(t, 7, counters.steal)

	path := filepath.Join(t.TempDir(), "cpu-pressure")
	require.NoError(t, os.WriteFile(path, []byte("some avg10=1.00 avg60=37.40 avg300=5.00 total=1\n"), 0o600))
	value := readCPUPSIAvg60(path)
	require.NotNil(t, value)
	assert.Equal(t, 37.4, *value)
}
