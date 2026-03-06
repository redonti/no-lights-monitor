package outage

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ukrainianWeekdays = [7]string{
	"Неділя", "Понеділок", "Вівторок", "Середа", "Четвер", "П'ятниця", "Субота",
}

type outageBlock struct {
	startH, startM, endH, endM int
}

// allOutageBlocks converts the hourly fact map into a list of contiguous off-power blocks.
// Each hour is split into two 30-min slots to handle "first"/"second" transitional statuses.
func allOutageBlocks(hours map[string]string) []outageBlock {
	var off [48]bool
	for h := 0; h < 24; h++ {
		switch hours[strconv.Itoa(h+1)] {
		case "no":
			off[h*2], off[h*2+1] = true, true
		case "first":
			off[h*2] = true
		case "second":
			off[h*2+1] = true
		}
	}

	var blocks []outageBlock
	for i := 0; i < 48; {
		if off[i] {
			j := i + 1
			for j < 48 && off[j] {
				j++
			}
			endH, endM := j/2, (j%2)*30
			blocks = append(blocks, outageBlock{i / 2, (i % 2) * 30, endH, endM})
			i = j
		} else {
			i++
		}
	}
	return blocks
}

func formatBlockDuration(startH, startM, endH, endM int) string {
	totalMinutes := (endH*60 + endM) - (startH*60 + startM)
	if totalMinutes%60 == 0 {
		return fmt.Sprintf("≈%d год.", totalMinutes/60)
	}
	return fmt.Sprintf("≈%.1f год.", float64(totalMinutes)/60)
}

// BuildPhotoCaption builds the caption for an outage schedule photo.
// Example output:
//
//	Графік відключень на сьогодні, 06.03 (П'ятниця), черга 5.1:
//	09:00 - 12:00 (≈3 год.)
//	19:00 - 22:30 (≈3.5 год.)
func BuildPhotoCaption(group string, fact *GroupHourlyFact, now time.Time) string {
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	today := now.In(kyiv)
	dateStr := today.Format("02.01")
	weekday := ukrainianWeekdays[today.Weekday()]

	header := fmt.Sprintf("Графік відключень на сьогодні, %s (%s), черга %s:", dateStr, weekday, group)

	blocks := allOutageBlocks(fact.Hours)
	if len(blocks) == 0 {
		return header + "\nВідключень не заплановано"
	}

	var sb strings.Builder
	sb.WriteString(header)
	for _, b := range blocks {
		endStr := fmt.Sprintf("%02d:%02d", b.endH, b.endM)
		if b.endH == 24 {
			endStr = "24:00"
		}
		dur := formatBlockDuration(b.startH, b.startM, b.endH, b.endM)
		sb.WriteString(fmt.Sprintf("\n%02d:%02d - %s (%s)", b.startH, b.startM, endStr, dur))
	}
	return sb.String()
}
