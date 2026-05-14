package tui

import (
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type SysStats struct {
	CPUUsage float64
	RAMUsage float64
	RAMUsed  uint64
	RAMTotal uint64
}

func PollSysStats() SysStats {
	var stats SysStats

	percentages, err := cpu.Percent(0, false)
	if err == nil && len(percentages) > 0 {
		stats.CPUUsage = percentages[0]
	}

	vmStat, err := mem.VirtualMemory()
	if err == nil {
		stats.RAMUsage = vmStat.UsedPercent
		stats.RAMUsed = vmStat.Used
		stats.RAMTotal = vmStat.Total
	}

	return stats
}
