package common

import (
	"os"
	"strconv"
	"strings"
)

type hostCPUCounters struct {
	total uint64
	steal uint64
}

// sampleHostCPUSteal 按增量计算 Linux CPU steal 百分比。
func (collector *systemResourceCollector) sampleHostCPUSteal(path string) *float64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	current, ok := parseHostCPUCounters(string(raw))
	if !ok {
		return nil
	}

	collector.hostStatMu.Lock()
	previous := collector.previousHost
	collector.previousHost = &current
	collector.hostStatMu.Unlock()
	if previous == nil || current.total <= previous.total || current.steal < previous.steal {
		return nil
	}
	percent := float64(current.steal-previous.steal) / float64(current.total-previous.total) * 100
	return float64Ptr(clampPercent(percent))
}

// parseHostCPUCounters 解析 Linux /proc/stat 的 CPU 汇总行。
func parseHostCPUCounters(raw string) (hostCPUCounters, bool) {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "cpu" {
			continue
		}
		result := hostCPUCounters{}
		for index, field := range fields[1:] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return hostCPUCounters{}, false
			}
			// guest 与 guest_nice 已包含在 user/nice 中，避免重复计入总时间。
			if index <= 7 {
				result.total += value
			}
			if index == 7 {
				result.steal = value
			}
		}
		return result, true
	}
	return hostCPUCounters{}, false
}

// readCPUPSIAvg60 读取 Linux CPU 压力的 some/avg60 值。
func readCPUPSIAvg60(path string) *float64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "avg60=") {
				continue
			}
			value, parseErr := strconv.ParseFloat(strings.TrimPrefix(field, "avg60="), 64)
			if parseErr == nil {
				return float64Ptr(clampPercent(value))
			}
			return nil
		}
	}
	return nil
}
