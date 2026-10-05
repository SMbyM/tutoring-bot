// Package telegram — адаптер Telegram: апдейты → ui.Input, msg.Message → сообщения с инлайн-кнопками.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/SMbyM/tutoring-bot/msg"
	"github.com/SMbyM/tutoring-bot/outbox"
	"github.com/SMbyM/tutoring-bot/store"
	"github.com/SMbyM/tutoring-bot/ui"
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

// ---- outbox.Handler ----

func (a *Adapter) Provider() string { return Provider }

// Handle выполняет запись outbox: сообщение, выдачу ссылки в канал или исключение из канала.
func (a *Adapter) Handle(ctx context.Context, it store.OutboxItem) error {
	switch it.Kind {
	case store.OutMessage:
		return a.sendTo(ctx, it.ChatID, it.Message)
	case store.OutChannelInvite:
		if a.channelID == 0 {
			return outbox.Permanent(errors.New("CHANNEL_ID не задан"))
		}
		link, err := a.b.CreateChatInviteLink(ctx, &bot.CreateChatInviteLinkParams{ChatID: a.channelID,
			Name: "u" + it.ExternalID, MemberLimit: 1, ExpireDate: int(time.Now().Add(24 * time.Hour).Unix())})
		if err != nil {
			return classify(err)
		}
		m := it.Message
		m.Buttons = append(m.Buttons, msg.Row(msg.Link("Вступить в канал", link.InviteLink)))
		return a.sendTo(ctx, it.ChatID, m)
	case store.OutChannelKick:
		if a.channelID == 0 {
			return nil
		}
		uid, err := strconv.ParseInt(it.ExternalID, 10, 64)
		if err != nil {
			return outbox.Permanent(err)
		}
		// ban + unban: исключить, но не банить навсегда
		if _, err := a.b.BanChatMember(ctx, &bot.BanChatMemberParams{ChatID: a.channelID, UserID: uid}); err != nil {
			return classify(err)
		}
		_, err = a.b.UnbanChatMember(ctx, &bot.UnbanChatMemberParams{ChatID: a.channelID, UserID: uid, OnlyIfBanned: true})
		return classify(err)
	}
	return outbox.Permanent(fmt.Errorf("неизвестный вид записи %q", it.Kind))
}

func (a *Adapter) sendTo(ctx context.Context, chatID string, m msg.Message) error {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return outbox.Permanent(err)
	}
	return classify(a.send(ctx, id, m))
}

// classify: заблокировал бота / неверный запрос — не повторяем; лимит — повторяем, когда скажет Telegram.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var tm *bot.TooManyRequestsError
	if errors.As(err, &tm) {
		return outbox.RetryAfter{Err: err, After: time.Duration(tm.RetryAfter+1) * time.Second}
	}
	if errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorNotFound) {
		return outbox.Permanent(err)
	}
	return err
}
