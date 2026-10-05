-- Последняя активность в мессенджере: уведомления идут туда, где пользователь был последним.
ALTER TABLE identities ADD COLUMN last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Outbox: ядро (бот, воркер, будущий сайт) пишет сюда намерения «отправить сообщение»,
-- «выдать ссылку в канал», «исключить из канала». Адаптер конкретного мессенджера
-- забирает свои строки (FOR UPDATE SKIP LOCKED) и выполняет их с ретраями.
CREATE TABLE outbox (
    id              BIGSERIAL PRIMARY KEY,
    provider        TEXT        NOT NULL,             -- tg, vk, …
    kind            TEXT        NOT NULL CHECK (kind IN ('message', 'channel_invite', 'channel_kick')),
    chat_id         TEXT        NOT NULL DEFAULT '',  -- куда писать
    external_id     TEXT        NOT NULL DEFAULT '',  -- кого исключать из канала
    payload         JSONB       NOT NULL DEFAULT '{}',
    status          TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts        INT         NOT NULL DEFAULT 0,
    last_error      TEXT        NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);
CREATE INDEX outbox_pending ON outbox(provider, next_attempt_at, id) WHERE status = 'pending';
