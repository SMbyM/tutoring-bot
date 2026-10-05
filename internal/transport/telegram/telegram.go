// Package telegram — адаптер Telegram: апдейты → ui.Input, msg.Message → сообщения с инлайн-кнопками.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/SMbyM/tutoring-bot/internal/msg"
	"github.com/SMbyM/tutoring-bot/internal/ui"
)

const Provider = "tg"

type Adapter struct {
	b         *bot.Bot
	eng       *ui.Engine
	channelID int64
	username  string
	log       *slog.Logger
}

// New подключается к Bot API. apiURL — свой адрес Bot API (через туннель/прокси), пусто — стандартный.
func New(ctx context.Context, token, apiURL string, eng *ui.Engine, channelID int64) (*Adapter, error) {
	a := &Adapter{eng: eng, channelID: channelID, log: slog.Default()}
	opts := []bot.Option{bot.WithDefaultHandler(a.handle), bot.WithErrorsHandler(func(err error) {
		a.log.Warn("telegram", "err", err)
	})}
	if apiURL != "" {
		opts = append(opts, bot.WithServerURL(apiURL))
	}
	b, err := bot.New(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	a.b = b
	me, err := b.GetMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("telegram getMe: %w", err)
	}
	a.username = me.Username
	eng.BotLink = func(payload string) string { return "https://t.me/" + a.username + "?start=" + payload }
	_, err = b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: []models.BotCommand{
		{Command: "menu", Description: "Главное меню"},
		{Command: "cancel", Description: "Отменить ввод"},
	}})
	if err != nil {
		a.log.Warn("не удалось задать команды", "err", err)
	}
	return a, nil
}

// Start запускает long polling (блокирует до отмены ctx).
func (a *Adapter) Start(ctx context.Context) { a.b.Start(ctx) }

func (a *Adapter) Username() string { return a.username }

func (a *Adapter) handle(ctx context.Context, b *bot.Bot, upd *models.Update) {
	switch {
	case upd.Message != nil:
		m := upd.Message
		if m.Chat.Type != models.ChatTypePrivate || m.From == nil {
			return
		}
		in := ui.Input{Provider: Provider, ExternalID: strconv.FormatInt(m.From.ID, 10), ChatID: strconv.FormatInt(m.Chat.ID, 10),
			Username: m.From.Username, FirstName: m.From.FirstName, Text: m.Text}
		a.render(ctx, m.Chat.ID, 0, "", a.eng.Handle(ctx, in))
	case upd.CallbackQuery != nil:
		cq := upd.CallbackQuery
		var chatID int64
		var msgID int
		if cq.Message.Message != nil {
			chatID, msgID = cq.Message.Message.Chat.ID, cq.Message.Message.ID
		} else if cq.Message.InaccessibleMessage != nil {
			chatID = cq.Message.InaccessibleMessage.Chat.ID
		} else {
			chatID = cq.From.ID
		}
		in := ui.Input{Provider: Provider, ExternalID: strconv.FormatInt(cq.From.ID, 10), ChatID: strconv.FormatInt(chatID, 10),
			Username: cq.From.Username, FirstName: cq.From.FirstName, Action: cq.Data}
		a.render(ctx, chatID, msgID, cq.ID, a.eng.Handle(ctx, in))
	}
}

func (a *Adapter) render(ctx context.Context, chatID int64, srcMsgID int, callbackID string, out []msg.Message) {
	toast := ""
	for _, m := range out {
		if m.Text == "" {
			if m.Toast != "" {
				toast = m.Toast
			}
			continue
		}
		if m.Replace && srcMsgID != 0 {
			_, err := a.b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: srcMsgID,
				Text: m.Text, ReplyMarkup: keyboard(m.Buttons)})
			if err == nil {
				continue
			}
			// например, «message is not modified» или сообщение слишком старое — шлём новым
		}
		if err := a.send(ctx, chatID, m); err != nil {
			a.log.Warn("отправка сообщения", "chat", chatID, "err", err)
		}
	}
	if callbackID != "" {
		_, _ = a.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callbackID, Text: toast})
	}
}

func (a *Adapter) send(ctx context.Context, chatID int64, m msg.Message) error {
	p := &bot.SendMessageParams{ChatID: chatID, Text: m.Text,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()}}
	if kb := keyboard(m.Buttons); kb != nil {
		p.ReplyMarkup = kb
	}
	_, err := a.b.SendMessage(ctx, p)
	return err
}

func keyboard(rows [][]msg.Button) models.ReplyMarkup {
	var kb [][]models.InlineKeyboardButton
	for _, row := range rows {
		var r []models.InlineKeyboardButton
		for _, b := range row {
			btn := models.InlineKeyboardButton{Text: b.Text}
			switch {
			case b.WebApp != "":
				btn.WebApp = &models.WebAppInfo{URL: b.WebApp}
			case b.URL != "":
				btn.URL = b.URL
			default:
				btn.CallbackData = b.Action
			}
			r = append(r, btn)
		}
		if len(r) > 0 {
			kb = append(kb, r)
		}
	}
	if len(kb) == 0 {
		return nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: kb}
}

// ---- core.Sender ----

func (a *Adapter) Provider() string { return Provider }

func (a *Adapter) Send(ctx context.Context, chatID string, m msg.Message) error {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return err
	}
	return a.send(ctx, id, m)
}

// ---- core.ChannelGate ----

// Gate управляет закрытым каналом. Бот должен быть админом канала с правом приглашать и блокировать.
type Gate struct{ a *Adapter }

func (a *Adapter) Gate() *Gate { return &Gate{a} }

func (g *Gate) Provider() string { return Provider }

func (g *Gate) InviteLink(ctx context.Context, name string) (string, error) {
	link, err := g.a.b.CreateChatInviteLink(ctx, &bot.CreateChatInviteLinkParams{ChatID: g.a.channelID, Name: name,
		MemberLimit: 1, ExpireDate: int(time.Now().Add(24 * time.Hour).Unix())})
	if err != nil {
		return "", err
	}
	return link.InviteLink, nil
}

// Kick исключает из канала без вечного бана: ban + unban.
func (g *Gate) Kick(ctx context.Context, externalID string) error {
	uid, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil {
		return err
	}
	if _, err := g.a.b.BanChatMember(ctx, &bot.BanChatMemberParams{ChatID: g.a.channelID, UserID: uid}); err != nil {
		return err
	}
	_, err = g.a.b.UnbanChatMember(ctx, &bot.UnbanChatMemberParams{ChatID: g.a.channelID, UserID: uid, OnlyIfBanned: true})
	return err
}
