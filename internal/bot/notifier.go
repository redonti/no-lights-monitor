package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"time"

	"no-lights-monitor/internal/database"
	"no-lights-monitor/internal/models"
	"no-lights-monitor/internal/outage"

	tele "gopkg.in/telebot.v3"
)

// TelegramNotifier implements heartbeat.Notifier using the Telegram bot.
type TelegramNotifier struct {
	bot          *tele.Bot
	db           *database.DB
	outageClient *outage.Client
}

func NewNotifier(b *tele.Bot, db *database.DB, oc *outage.Client) *TelegramNotifier {
	return &TelegramNotifier{bot: b, db: db, outageClient: oc}
}

// NotifyStatusChange sends a status message to the linked Telegram channel.
// On channel access errors the monitor is paused and the owner is notified via DM.
func (n *TelegramNotifier) NotifyStatusChange(monitorID, channelID int64, name, address string, notifyAddress, isOnline bool, duration time.Duration, when time.Time, outageRegion, outageGroup string, notifyOutage bool) {
	var msg string
	dur := database.FormatDuration(duration)
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	timeStr := when.In(kyiv).Format("15:04")

	if isOnline {
		msg = fmt.Sprintf(msgNotifyOnline, timeStr, dur)
	} else {
		msg = fmt.Sprintf(msgNotifyOffline, timeStr, dur)
	}

	if notifyAddress && address != "" {
		msg += fmt.Sprintf(msgNotifyAddressLine, html.EscapeString(address))
	}

	// Append outage schedule info if enabled.
	if notifyOutage && outageRegion != "" && outageGroup != "" && n.outageClient != nil {
		if outageLine := n.buildOutageLine(outageRegion, outageGroup, isOnline, when); outageLine != "" {
			msg += outageLine
		}
	}

	chat := &tele.Chat{ID: channelID}
	_, err := n.bot.Send(chat, msg, htmlOpts)
	if err != nil {
		ctx := context.Background()
		ownerID, dbErr := n.db.GetOwnerTelegramIDByMonitorID(ctx, monitorID)
		if dbErr != nil {
			log.Printf("[bot] failed to get owner for monitor %d: %v", monitorID, dbErr)
			return
		}
		monitor := &models.Monitor{ID: monitorID, Name: name}
		if !NotifyChannelError(ctx, n.bot, n.db, err, ownerID, monitor) {
			log.Printf("[bot] failed to send notification to channel %d: %v", channelID, err)
		}
	}
}

// buildOutageLine fetches the outage schedule and builds the notification line.
// For lights ON: shows next planned outage window.
// For lights OFF: shows expected restoration time.
func (n *TelegramNotifier) buildOutageLine(region, group string, isOnline bool, when time.Time) string {
	fact, err := n.outageClient.GetGroupFact(region, group)
	if err != nil {
		log.Printf("[bot] outage fetch error for %s/%s: %v", region, group, err)
		return ""
	}

	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	nowKyiv := when.In(kyiv)
	currentHour := nowKyiv.Hour() // 0-23

	log.Printf("[bot] outage data for %s/%s: factUpdate=%s, date=%s, currentHour=%d, isOnline=%v, hours=%v",
		region, group, fact.FactUpdate, fact.Date, currentHour, isOnline, fact.Hours)

	// Check if schedule matches actual status. If not, this is likely an
	// unplanned event — the schedule can't predict it, so skip the outage line.
	// We check both current and next hour to handle threshold drift
	// (e.g. outage scheduled at 15:00 but power cuts at 14:55).
	// "first" = off first 30 min, on second 30 min (transitional).
	// "second" = on first 30 min, off second 30 min (transitional).
	// Both count as matching either on or off, since status can change mid-hour.
	isOffHour := func(h int) bool {
		s := fact.Hours[strconv.Itoa(h+1)]
		return s == "no" || s == "first" || s == "second"
	}
	isOnHour := func(h int) bool {
		s := fact.Hours[strconv.Itoa(h+1)]
		return s == "yes" || s == "first" || s == "second"
	}
	nextHour := currentHour + 1
	if nextHour >= 24 {
		nextHour = 23
	}
	curStatus := fact.Hours[strconv.Itoa(currentHour+1)]
	nextStatus := fact.Hours[strconv.Itoa(nextHour+1)]
	if isOnline && !isOnHour(currentHour) && !isOnHour(nextHour) {
		log.Printf("[bot] outage skip: lights ON but schedule says off (cur=%q next=%q) — unplanned", curStatus, nextStatus)
		return ""
	}
	if !isOnline && !isOffHour(currentHour) && !isOffHour(nextHour) {
		log.Printf("[bot] outage skip: lights OFF but schedule says on (cur=%q next=%q) — unplanned", curStatus, nextStatus)
		return ""
	}

	if isOnline {
		// Find next contiguous outage block, only within today (no wrap-around).
		startH, startM, endH, endM, ok := findNextOutageBlock(fact.Hours, currentHour)
		if !ok {
			log.Printf("[bot] outage: lights ON, no next outage block found today")
			return ""
		}
		startStr := fmt.Sprintf("%02d:%02d", startH, startM)
		endStr := fmt.Sprintf("%02d:%02d", endH, endM)
		if endH == 24 {
			endStr = "24:00"
		}
		log.Printf("[bot] outage: lights ON, next outage block %s-%s", startStr, endStr)
		return fmt.Sprintf(msgOutageNextPlanned, fmt.Sprintf("%s - %s", startStr, endStr))
	}

	// Lights OFF: find next restoration (full "yes" hour or "first" at :30).
	restoreH, restoreM, ok := findNextRestoration(fact.Hours, currentHour)
	if !ok {
		log.Printf("[bot] outage: lights OFF, no restoration found today")
		return ""
	}
	restoreTime := time.Date(nowKyiv.Year(), nowKyiv.Month(), nowKyiv.Day(), restoreH, restoreM, 0, 0, nowKyiv.Location())
	if restoreTime.Before(nowKyiv) {
		restoreTime = restoreTime.Add(24 * time.Hour)
	}
	durationUntil := restoreTime.Sub(nowKyiv)
	durStr := database.FormatDuration(durationUntil)
	restoreStr := fmt.Sprintf("%02d:%02d", restoreH, restoreM)
	log.Printf("[bot] outage: lights OFF, next ON at %s (in %s)", restoreStr, durStr)
	return fmt.Sprintf(msgOutageExpected, durStr, restoreStr)
}

// findNextOutageBlock finds the next contiguous block of outage hours
// (status "no", "first", or "second") starting from the given hour.
// Handles transitional hours: "first" (off 00-30) ends block at :30,
// "second" (off 30-60) starts block at :30.
// Returns (startH, startM, endH, endM, ok). endH may be 24 for midnight.
func findNextOutageBlock(hours map[string]string, currentHour int) (startH, startM, endH, endM int, ok bool) {
	h := currentHour + 1

	// If we just got lights ON early during a scheduled outage block,
	// we should skip the remaining hours of this current block
	// so we don't report them as the "next" outage block.
	curKey := strconv.Itoa(currentHour + 1)
	curStatus := hours[curKey]
	if curStatus == "no" || curStatus == "first" || curStatus == "second" {
		for ; h < 24; h++ {
			st := hours[strconv.Itoa(h+1)]
			if st == "yes" {
				break
			}
		}
	}

	for ; h < 24; h++ {
		hourKey := strconv.Itoa(h + 1) // hours in data are 1-24
		status := hours[hourKey]
		if status == "no" || status == "first" || status == "second" {
			// Block start: "second" => :30, else :00
			if status == "second" {
				startH, startM = h, 30
			} else {
				startH, startM = h, 0
			}
			// Block end for first hour
			if status == "first" {
				endH, endM = h, 30
				return startH, startM, endH, endM, true
			}
			// "no" or "second": block continues
			endH, endM = h+1, 0
			for nextH := h + 1; nextH < 24; nextH++ {
				nextKey := strconv.Itoa(nextH + 1)
				nextStatus := hours[nextKey]
				if nextStatus == "no" || nextStatus == "second" {
					endH, endM = nextH+1, 0
				} else if nextStatus == "first" {
					endH, endM = nextH, 30
					return startH, startM, endH, endM, true
				} else {
					break
				}
			}
			return startH, startM, endH, endM, true
		}
	}
	return 0, 0, 0, 0, false
}

// findNextRestoration finds the next time power returns: "yes" (full hour) or "first" (at :30).
// Returns (hour, minute, ok). Hour is 0-23, minute is 0 or 30.
func findNextRestoration(hours map[string]string, currentHour int) (hour, minute int, ok bool) {
	for h := currentHour + 1; h < 24; h++ {
		hourKey := strconv.Itoa(h + 1)
		status := hours[hourKey]
		if status == "yes" {
			return h, 0, true
		}
		if status == "first" {
			return h, 30, true
		}
	}
	return 0, 0, false
}

// ── Channel error helpers ─────────────────────────────────────────────

// isChannelError reports whether a Telegram API error means the bot lost access to a channel.
func isChannelError(err error) bool {
	return errors.Is(err, tele.ErrChatNotFound) ||
		errors.Is(err, tele.ErrKickedFromGroup) ||
		errors.Is(err, tele.ErrKickedFromSuperGroup) ||
		errors.Is(err, tele.ErrKickedFromChannel) ||
		errors.Is(err, tele.ErrNotChannelMember) ||
		errors.Is(err, tele.ErrNoRightsToSend) ||
		errors.Is(err, tele.ErrNoRightsToSendPhoto)
}

// SendToUser sends an HTML message directly to a Telegram user by their Telegram ID.
func SendToUser(b *tele.Bot, userTelegramID int64, msg string) {
	chat := &tele.Chat{ID: userTelegramID}
	if _, err := b.Send(chat, msg, htmlOpts); err != nil {
		log.Printf("[bot] failed to send DM to user %d: %v", userTelegramID, err)
	}
}

// NotifyChannelError checks whether err is a channel access error and, if so,
// pauses the monitor in the DB and notifies the owner.
// Returns true if the error was a channel error and was handled.
func NotifyChannelError(ctx context.Context, b *tele.Bot, db *database.DB, err error, userTelegramID int64, monitor *models.Monitor) bool {
	if !isChannelError(err) {
		return false
	}
	log.Printf("[bot] channel access lost for monitor %d (%s), pausing", monitor.ID, monitor.Name)
	// Attempt to notify the channel — may succeed for partial-access errors (e.g. no photo rights).
	if monitor.ChannelID != 0 {
		chat := &tele.Chat{ID: monitor.ChannelID}
		if _, sendErr := b.Send(chat, msgChannelPausedBySystem, htmlOpts); sendErr != nil {
			log.Printf("[bot] could not send system-pause notice to channel %d: %v", monitor.ChannelID, sendErr)
		}
	}
	if dbErr := db.SetMonitorActive(ctx, monitor.ID, false); dbErr != nil {
		log.Printf("[bot] failed to pause monitor %d: %v", monitor.ID, dbErr)
	}
	msg := fmt.Sprintf(msgChannelError, html.EscapeString(monitor.Name))
	SendToUser(b, userTelegramID, msg)
	return true
}
