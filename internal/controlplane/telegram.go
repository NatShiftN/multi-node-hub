package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

type telegramController struct {
	server  *Server
	bot     *tele.Bot
	adminID int64
}

func (s *Server) runTelegramBot(ctx context.Context) {
	token := strings.TrimSpace(s.cfg.TelegramBotToken)
	if token == "" {
		return
	}
	if s.cfg.TelegramAdminID == 0 {
		s.logger.Warn("Telegram bot token is configured but TELEGRAM_ADMIN_ID is empty; bot disabled")
		return
	}
	bot, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
		OnError: func(err error, _ tele.Context) {
			s.logger.Warn("telegram bot error", "error", err)
		},
	})
	if err != nil {
		s.logger.Error("start Telegram bot", "error", err)
		return
	}
	controller := &telegramController{server: s, bot: bot, adminID: s.cfg.TelegramAdminID}
	bot.Handle("/stats", controller.onlyAdmin(controller.handleStats))
	bot.Handle("/nodes", controller.onlyAdmin(controller.handleNodes))
	bot.Handle("/reboot", controller.onlyAdmin(controller.handleReboot))
	bot.Handle("/ban", controller.onlyAdmin(controller.handleBan))
	bot.Handle("/help", controller.onlyAdmin(controller.handleHelp))
	bot.Handle(tele.OnText, controller.onlyAdmin(controller.handleHelp))
	go bot.Start()
	<-ctx.Done()
	bot.Stop()
}

func (c *telegramController) onlyAdmin(next tele.HandlerFunc) tele.HandlerFunc {
	return func(ctx tele.Context) error {
		sender := ctx.Sender()
		if sender == nil || sender.ID != c.adminID {
			return ctx.Send("Unauthorized. This bot is bound to a single NodeHub administrator.")
		}
		return next(ctx)
	}
}

func (c *telegramController) handleHelp(ctx tele.Context) error {
	return ctx.Send("NodeHub commands:\n/stats\n/nodes — abnormal nodes\n/reboot <node_id>\n/ban <email>")
}

func (c *telegramController) handleStats(ctx tele.Context) error {
	stats, err := c.server.dashboardStatsData()
	if err != nil {
		return ctx.Send("failed to read stats: " + err.Error())
	}
	text := fmt.Sprintf(
		"NodeHub stats\nOnline: %d / %d\nGFW blocked: %d\nOffline: %d\nMonth upload: %s\nMonth download: %s\nBanned users: %d\nAvg CPU: %.1f%%\nAvg RAM: %.1f%%",
		stats.NodesOnline, stats.NodesTotal, stats.NodesGFWBlocked, stats.NodesOffline,
		formatByteCount(stats.MonthlyUpload), formatByteCount(stats.MonthlyDownload),
		stats.BannedUsers, stats.AverageCPU, stats.AverageMemory,
	)
	return ctx.Send(text)
}

func (c *telegramController) handleNodes(ctx tele.Context) error {
	nodes, err := c.server.store.ListNodes()
	if err != nil {
		return ctx.Send("failed to read nodes")
	}
	lines := []string{"Abnormal nodes:"}
	for _, node := range nodes {
		if node.Status == "online" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s", node.NodeID, node.Name, node.Status))
	}
	if len(lines) == 1 {
		lines = append(lines, "none")
	}
	return ctx.Send(strings.Join(lines, "\n"))
}

func (c *telegramController) handleReboot(ctx tele.Context) error {
	args := ctx.Args()
	if len(args) != 1 {
		return ctx.Send("usage: /reboot <node_id>")
	}
	nodeID := strings.TrimSpace(args[0])
	if _, err := c.server.store.GetNode(nodeID); err != nil {
		return ctx.Send("unknown node: " + nodeID)
	}
	if _, online := c.server.hub.Get(nodeID); !online {
		return ctx.Send("node Agent is offline")
	}
	commandCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, _, err := c.server.hub.Execute(commandCtx, nodeID, "reboot", []byte(`{}`), 30*time.Second)
	if err != nil {
		return ctx.Send("reboot failed: " + err.Error())
	}
	if result.Error != "" {
		return ctx.Send("reboot rejected: " + result.Error)
	}
	c.server.auditSystem(fmt.Sprintf("telegram:%d", c.adminID), "reboot", nodeID, map[string]any{"command": "reboot"})
	return ctx.Send("reboot accepted: " + nodeID)
}

func (c *telegramController) handleBan(ctx tele.Context) error {
	args := ctx.Args()
	if len(args) != 1 {
		return ctx.Send("usage: /ban <email>")
	}
	email := strings.TrimSpace(args[0])
	nodeIDs, err := c.server.store.BanTrafficUser(email, time.Now().UTC())
	if err != nil {
		return ctx.Send("ban failed: " + err.Error())
	}
	for _, nodeID := range nodeIDs {
		c.server.enforceBannedUser(context.Background(), nodeID, email)
	}
	c.server.auditSystem(fmt.Sprintf("telegram:%d", c.adminID), "traffic_ban", "", map[string]any{
		"user_email": email, "node_ids": nodeIDs, "source": "telegram",
	})
	return ctx.Send(fmt.Sprintf("banned %s on %d node(s)", email, len(nodeIDs)))
}

func formatByteCount(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := uint64(unit), 0
	for n := value / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}
