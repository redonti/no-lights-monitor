package bot

import (
	"context"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"

	"no-lights-monitor/internal/models"

	tele "gopkg.in/telebot.v3"
)

func (b *Bot) handleCallback(c tele.Context) error {
	log.Printf("[bot] callback %q from user %d (@%s)", c.Callback().Data, c.Sender().ID, c.Sender().Username)
	data := c.Callback().Data
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return c.Respond(&tele.CallbackResponse{Text: msgInvalidFormat})
	}

	action := parts[0]

	// Handle create_type callback (no monitor ID needed).
	if action == "create_type" {
		return b.onCreateType(c, parts[1])
	}

	monitorID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: msgInvalidMonitor})
	}

	ctx := context.Background()

	// Get all monitors and find the right one
	monitors, err := b.db.GetMonitorsByTelegramID(ctx, c.Sender().ID)
	if err != nil {
		log.Printf("[bot] get monitors error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgFetchError})
	}

	var targetMonitor *models.Monitor
	for _, m := range monitors {
		if m.ID == monitorID {
			targetMonitor = m
			break
		}
	}

	if targetMonitor == nil {
		return c.Respond(&tele.CallbackResponse{Text: msgMonitorNotFound})
	}

	switch action {
	case "stop":
		return b.onCallbackStop(ctx, c, targetMonitor)
	case "resume":
		return b.onCallbackResume(ctx, c, targetMonitor)
	case "delete_confirm":
		return b.onCallbackDelete(ctx, c, targetMonitor)
	case "info":
		return b.onCallbackInfo(ctx, c, targetMonitor)
	case "edit":
		return b.onCallbackEdit(c, targetMonitor)
	case "edit_name":
		return b.onCallbackEditName(c, targetMonitor)
	case "edit_address":
		return b.onCallbackEditAddress(c, targetMonitor)
	case "edit_channel_refresh":
		return b.onCallbackEditChannelRefresh(ctx, c, targetMonitor)
	case "edit_notify_address":
		return b.onCallbackEditNotifyAddress(ctx, c, targetMonitor)
	case "edit_outage":
		return b.onCallbackEditOutage(c, targetMonitor)
	case "outage_r":
		return b.onCallbackOutageRegion(c, parts, targetMonitor)
	case "outage_g":
		return b.onCallbackOutageGroup(ctx, c, parts, targetMonitor)
	case "edit_notify_outage":
		return b.onCallbackEditNotifyOutage(ctx, c, targetMonitor)
	case "edit_outage_photo":
		return b.onCallbackEditOutagePhoto(ctx, c, targetMonitor)
	case "edit_graph":
		return b.onCallbackEditGraph(ctx, c, targetMonitor)
	case "map_hide":
		return b.onCallbackMapHide(ctx, c, targetMonitor)
	case "map_show":
		return b.onCallbackMapShow(ctx, c, targetMonitor)
	case "test":
		return b.onCallbackTest(c, targetMonitor)
	default:
		return c.Respond(&tele.CallbackResponse{Text: msgUnknownAction})
	}
}

func (b *Bot) onCallbackStop(ctx context.Context, c tele.Context, m *models.Monitor) error {
	if err := b.db.SetMonitorActive(ctx, m.ID, false); err != nil {
		log.Printf("[bot] set monitor inactive error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgStopError})
	}
	b.heartbeatSvc.SetMonitorActive(m.Token, false)
	if m.ChannelID != 0 {
		if _, err := b.bot.Send(&tele.Chat{ID: m.ChannelID}, msgChannelPaused, htmlOpts); err != nil {
			log.Printf("[bot] failed to send pause notice to channel %d: %v", m.ChannelID, err)
		}
	}
	_ = c.Respond(&tele.CallbackResponse{Text: msgStopOK})
	return c.Edit(fmt.Sprintf(msgStopDone, msgStopOK, html.EscapeString(m.Name)), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) onCallbackResume(ctx context.Context, c tele.Context, m *models.Monitor) error {
	// If there's a linked channel, verify the bot still has access before resuming.
	if m.ChannelID != 0 {
		chat := &tele.Chat{ID: m.ChannelID}
		me := b.bot.Me
		member, err := b.bot.ChatMemberOf(chat, me)
		if err != nil || (member.Role != tele.Administrator && member.Role != tele.Creator) || !member.Rights.CanPostMessages {
			_ = c.Respond(&tele.CallbackResponse{Text: msgResumeNoAccess})
			return c.Edit(fmt.Sprintf(msgResumeNoAccessDetail, html.EscapeString(m.ChannelName)), tele.ModeHTML, &tele.ReplyMarkup{})
		}
	}
	if err := b.db.SetMonitorActive(ctx, m.ID, true); err != nil {
		log.Printf("[bot] set monitor active error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgResumeError})
	}
	b.heartbeatSvc.SetMonitorActive(m.Token, true)
	if m.ChannelID != 0 {
		if _, err := b.bot.Send(&tele.Chat{ID: m.ChannelID}, msgChannelResumed, htmlOpts); err != nil {
			log.Printf("[bot] failed to send resume notice to channel %d: %v", m.ChannelID, err)
		}
	}
	_ = c.Respond(&tele.CallbackResponse{Text: msgResumeOK})
	return c.Edit(fmt.Sprintf(msgResumeDone, msgResumeOK, html.EscapeString(m.Name)), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) onCallbackDelete(ctx context.Context, c tele.Context, m *models.Monitor) error {
	if err := b.db.DeleteMonitor(ctx, m.ID); err != nil {
		log.Printf("[bot] delete monitor error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgDeleteError})
	}
	b.heartbeatSvc.RemoveMonitor(m.Token)
	_ = c.Respond(&tele.CallbackResponse{Text: msgDeleteOK})
	return c.Edit(fmt.Sprintf(msgDeleteDone, msgDeleteOK, html.EscapeString(m.Name)), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) onCallbackInfo(ctx context.Context, c tele.Context, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})

	var bld strings.Builder
	bld.WriteString(msgInfoDetailHeader)
	bld.WriteString(fmt.Sprintf(msgInfoDetailName, html.EscapeString(m.Name)))
	bld.WriteString(fmt.Sprintf(msgInfoDetailAddress, html.EscapeString(m.Address)))
	bld.WriteString(fmt.Sprintf(msgInfoDetailCoords, m.Latitude, m.Longitude))

	status := msgInfoStatusOffline
	if m.IsOnline {
		status = msgInfoStatusOnline
	}
	if !m.IsActive {
		status = msgStatusPaused
	}
	bld.WriteString(fmt.Sprintf(msgInfoDetailStatus, status))

	if m.LastHeartbeatAt != nil {
		bld.WriteString(fmt.Sprintf(msgInfoDetailLastPing, m.LastHeartbeatAt.Format("2006-01-02 15:04:05")))
	}

	if m.ChannelID != 0 {
		bld.WriteString(fmt.Sprintf(msgInfoDetailChannel, html.EscapeString(m.ChannelName)))
	} else {
		bld.WriteString("\n")
	}

	if m.MonitorType == "ping" {
		bld.WriteString(fmt.Sprintf(msgInfoDetailTypePing, msgInfoTypePing))
		bld.WriteString(fmt.Sprintf(msgInfoDetailTarget, html.EscapeString(m.PingTarget)))
		bld.WriteString(msgInfoPingHint)
	} else {
		bld.WriteString(fmt.Sprintf(msgInfoDetailTypeHB, msgInfoTypeHeartbeat))
		bld.WriteString(msgInfoDetailURLLabel)
		bld.WriteString(fmt.Sprintf(msgInfoDetailURL, b.baseURL, m.Token))
		bld.WriteString(msgInfoHeartbeatHint)
	}

	bld.WriteString("\n")
	bld.WriteString(fmt.Sprintf(msgInfoDetailSettings, b.baseURL, m.SettingsToken))

	return c.Edit(bld.String(), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) renderEditMenu(c tele.Context, m *models.Monitor) error {
	addrBtnText := msgEditBtnHideAddress
	if !m.NotifyAddress {
		addrBtnText = msgEditBtnShowAddress
	}
	mapBtnText := msgMapBtnHide
	mapBtnAction := "map_hide"
	if !m.IsPublic {
		mapBtnText = msgMapBtnShow
		mapBtnAction = "map_show"
	}
	rows := [][]tele.InlineButton{
		{{Text: msgEditBtnName, Data: fmt.Sprintf("edit_name:%d", m.ID)}},
		{{Text: msgEditBtnAddress, Data: fmt.Sprintf("edit_address:%d", m.ID)}},
		{{Text: addrBtnText, Data: fmt.Sprintf("edit_notify_address:%d", m.ID)}},
		{{Text: mapBtnText, Data: fmt.Sprintf("%s:%d", mapBtnAction, m.ID)}},
	}
	if m.ChannelID != 0 {
		rows = append(rows, []tele.InlineButton{
			{Text: msgEditBtnRefreshChannel, Data: fmt.Sprintf("edit_channel_refresh:%d", m.ID)},
		})
		// Graph toggle.
		graphBtnText := msgEditBtnShowGraph
		if m.GraphEnabled {
			graphBtnText = msgEditBtnHideGraph
		}
		rows = append(rows, []tele.InlineButton{
			{Text: graphBtnText, Data: fmt.Sprintf("edit_graph:%d", m.ID)},
		})
	}
	// Outage group button.
	rows = append(rows, []tele.InlineButton{
		{Text: msgEditBtnOutage, Data: fmt.Sprintf("edit_outage:%d", m.ID)},
	})
	// Outage notify toggle (only if group is set).
	if m.OutageGroup != "" {
		outageBtnText := msgEditBtnShowOutage
		if m.NotifyOutage {
			outageBtnText = msgEditBtnHideOutage
		}
		rows = append(rows, []tele.InlineButton{
			{Text: outageBtnText, Data: fmt.Sprintf("edit_notify_outage:%d", m.ID)},
		})
		// Outage photo toggle (only if group is set and channel linked).
		if m.ChannelID != 0 {
			photoBtnText := msgEditBtnShowOutagePhoto
			if m.OutagePhotoEnabled {
				photoBtnText = msgEditBtnHideOutagePhoto
			}
			rows = append(rows, []tele.InlineButton{
				{Text: photoBtnText, Data: fmt.Sprintf("edit_outage_photo:%d", m.ID)},
			})
		}
	}
	keyboard := &tele.ReplyMarkup{InlineKeyboard: rows}
	return c.Edit(fmt.Sprintf(msgEditChoose, html.EscapeString(m.Name)), tele.ModeHTML, keyboard)
}

func (b *Bot) onCallbackEdit(c tele.Context, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})
	return b.renderEditMenu(c, m)
}

func (b *Bot) onCallbackEditName(c tele.Context, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})
	b.mu.Lock()
	b.conversations[c.Sender().ID] = &conversationData{
		State:         stateAwaitingEditName,
		EditMonitorID: m.ID,
	}
	b.mu.Unlock()
	return c.Edit(fmt.Sprintf(msgEditNamePrompt, html.EscapeString(m.Name)), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) onCallbackEditAddress(c tele.Context, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})
	b.mu.Lock()
	b.conversations[c.Sender().ID] = &conversationData{
		State:         stateAwaitingEditAddress,
		EditMonitorID: m.ID,
	}
	b.mu.Unlock()
	return c.Edit(fmt.Sprintf(msgEditAddressPrompt, html.EscapeString(m.Address)), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) onCallbackEditChannelRefresh(ctx context.Context, c tele.Context, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})
	chat, err := b.bot.ChatByID(m.ChannelID)
	if err != nil {
		log.Printf("[bot] failed to fetch channel info for monitor %d: %v", m.ID, err)
		return c.Edit(msgEditChannelRefreshError, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	newName := chat.Username
	if newName == m.ChannelName {
		return c.Edit(fmt.Sprintf(msgEditChannelRefreshNoChange, newName), tele.ModeHTML, &tele.ReplyMarkup{})
	}
	if err := b.db.UpdateMonitorChannelName(ctx, m.ID, newName); err != nil {
		log.Printf("[bot] failed to update channel name for monitor %d: %v", m.ID, err)
		return c.Edit(msgError, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	return c.Edit(fmt.Sprintf(msgEditChannelRefreshDone, newName), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) onCallbackEditNotifyAddress(ctx context.Context, c tele.Context, m *models.Monitor) error {
	newVal := !m.NotifyAddress
	if err := b.db.SetMonitorNotifyAddress(ctx, m.ID, newVal); err != nil {
		log.Printf("[bot] set notify_address error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgNotifyAddressError})
	}
	// Update in-memory state in heartbeat service.
	b.heartbeatSvc.SetMonitorNotifyAddress(m.Token, newVal)
	_ = c.Respond(&tele.CallbackResponse{})
	m.NotifyAddress = newVal
	return b.renderEditMenu(c, m)
}

func (b *Bot) onCallbackEditOutage(c tele.Context, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})
	if b.outageClient == nil {
		return c.Edit(msgOutageGroupError, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	regions, err := b.outageClient.GetRegions()
	if err != nil {
		log.Printf("[bot] outage get regions error: %v", err)
		return c.Edit(msgOutageGroupError, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	var regionRows [][]tele.InlineButton
	for _, r := range regions {
		regionRows = append(regionRows, []tele.InlineButton{
			{Text: r.RegionID, Data: fmt.Sprintf("outage_r:%d:%s", m.ID, r.RegionID)},
		})
	}
	keyboard := &tele.ReplyMarkup{InlineKeyboard: regionRows}
	return c.Edit(msgOutageRegionPrompt, tele.ModeHTML, keyboard)
}

func (b *Bot) onCallbackOutageRegion(c tele.Context, parts []string, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})
	if len(parts) < 3 {
		return c.Edit(msgInvalidFormat, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	region := parts[2]
	if b.outageClient == nil {
		return c.Edit(msgOutageGroupError, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	groups, err := b.outageClient.GetGroups(region)
	if err != nil {
		log.Printf("[bot] outage get groups error: %v", err)
		return c.Edit(msgOutageGroupError, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	var groupRows [][]tele.InlineButton
	// Show groups in rows of 3 buttons.
	for i := 0; i < len(groups); i += 3 {
		var row []tele.InlineButton
		for j := i; j < i+3 && j < len(groups); j++ {
			row = append(row, tele.InlineButton{
				Text: groups[j].Name,
				Data: fmt.Sprintf("outage_g:%d:%s:%s", m.ID, region, groups[j].ID),
			})
		}
		groupRows = append(groupRows, row)
	}
	keyboard := &tele.ReplyMarkup{InlineKeyboard: groupRows}
	return c.Edit(msgOutageGroupPrompt, tele.ModeHTML, keyboard)
}

func (b *Bot) onCallbackOutageGroup(ctx context.Context, c tele.Context, parts []string, m *models.Monitor) error {
	_ = c.Respond(&tele.CallbackResponse{})
	if len(parts) < 4 {
		return c.Edit(msgInvalidFormat, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	region := parts[2]
	group := parts[3]
	if err := b.db.SetMonitorOutageGroup(ctx, m.ID, region, group); err != nil {
		log.Printf("[bot] set outage group error: %v", err)
		return c.Edit(msgError, tele.ModeHTML, &tele.ReplyMarkup{})
	}
	b.heartbeatSvc.SetMonitorOutageGroup(m.Token, region, group)
	// Auto-enable notify_outage when setting a group.
	if err := b.db.SetMonitorNotifyOutage(ctx, m.ID, true); err != nil {
		log.Printf("[bot] set notify_outage error: %v", err)
	}
	b.heartbeatSvc.SetMonitorNotifyOutage(m.Token, true)
	return c.Edit(fmt.Sprintf(msgOutageGroupSet, html.EscapeString(group), html.EscapeString(region)), tele.ModeHTML, &tele.ReplyMarkup{})
}

func (b *Bot) onCallbackEditNotifyOutage(ctx context.Context, c tele.Context, m *models.Monitor) error {
	newVal := !m.NotifyOutage
	if err := b.db.SetMonitorNotifyOutage(ctx, m.ID, newVal); err != nil {
		log.Printf("[bot] set notify_outage error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgNotifyOutageError})
	}
	b.heartbeatSvc.SetMonitorNotifyOutage(m.Token, newVal)
	_ = c.Respond(&tele.CallbackResponse{})
	m.NotifyOutage = newVal
	return b.renderEditMenu(c, m)
}

func (b *Bot) onCallbackEditGraph(ctx context.Context, c tele.Context, m *models.Monitor) error {
	newVal := !m.GraphEnabled
	if err := b.db.SetMonitorGraphEnabled(ctx, m.ID, newVal); err != nil {
		log.Printf("[bot] set graph_enabled error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgGraphToggleError})
	}
	_ = c.Respond(&tele.CallbackResponse{})
	m.GraphEnabled = newVal
	return b.renderEditMenu(c, m)
}

func (b *Bot) onCallbackEditOutagePhoto(ctx context.Context, c tele.Context, m *models.Monitor) error {
	newVal := !m.OutagePhotoEnabled
	if err := b.db.SetMonitorOutagePhotoEnabled(ctx, m.ID, newVal); err != nil {
		log.Printf("[bot] set outage_photo_enabled error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgOutagePhotoError})
	}
	_ = c.Respond(&tele.CallbackResponse{})
	m.OutagePhotoEnabled = newVal
	return b.renderEditMenu(c, m)
}

func (b *Bot) onCallbackMapHide(ctx context.Context, c tele.Context, m *models.Monitor) error {
	if err := b.db.SetMonitorPublic(ctx, m.ID, false); err != nil {
		log.Printf("[bot] set monitor public error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgMapHideError})
	}
	_ = c.Respond(&tele.CallbackResponse{})
	m.IsPublic = false
	return b.renderEditMenu(c, m)
}

func (b *Bot) onCallbackMapShow(ctx context.Context, c tele.Context, m *models.Monitor) error {
	if err := b.db.SetMonitorPublic(ctx, m.ID, true); err != nil {
		log.Printf("[bot] set monitor public error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgMapHideError})
	}
	_ = c.Respond(&tele.CallbackResponse{})
	m.IsPublic = true
	return b.renderEditMenu(c, m)
}

func (b *Bot) onCallbackTest(c tele.Context, m *models.Monitor) error {
	if m.ChannelID == 0 {
		return c.Respond(&tele.CallbackResponse{Text: msgTestNoChannel})
	}

	testMsg := fmt.Sprintf(msgTestNotification,
		html.EscapeString(m.Name),
		html.EscapeString(m.Address),
	)

	chat := &tele.Chat{ID: m.ChannelID}
	if _, err := b.bot.Send(chat, testMsg, htmlOpts); err != nil {
		log.Printf("[bot] test notification error: %v", err)
		return c.Respond(&tele.CallbackResponse{Text: msgTestSendError})
	}

	_ = c.Respond(&tele.CallbackResponse{Text: msgTestOK})
	return c.Edit(fmt.Sprintf(msgTestSentTo, msgTestOK, html.EscapeString(m.ChannelName)), tele.ModeHTML, &tele.ReplyMarkup{})
}
