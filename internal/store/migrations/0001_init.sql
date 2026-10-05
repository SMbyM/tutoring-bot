-- Пользователи. Роль хранится в одном поле: один человек — одна роль.
-- debug_role переопределяет роль только при APP_ENV=debug.
CREATE TABLE users (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL DEFAULT '',
    role        TEXT        NOT NULL DEFAULT '' CHECK (role IN ('', 'student', 'parent', 'tutor', 'admin')),
    grade       SMALLINT    CHECK (grade BETWEEN 1 AND 11),
    tz          TEXT        NOT NULL DEFAULT 'Asia/Novosibirsk',
    managed_by  BIGINT      REFERENCES users(id), -- ребёнок без мессенджера: всё делает родитель
    consent_at  TIMESTAMPTZ,
    debug_role  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Привязка к мессенджерам. Один пользователь может иметь TG, VK, MAX и т.д.
CREATE TABLE identities (
    provider    TEXT   NOT NULL,
    external_id TEXT   NOT NULL,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id     TEXT   NOT NULL,
    username    TEXT   NOT NULL DEFAULT '',
    PRIMARY KEY (provider, external_id)
);
CREATE INDEX identities_user ON identities(user_id);

CREATE TABLE user_settings (
    user_id                 BIGINT  PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    remind_morning          BOOLEAN NOT NULL DEFAULT true,
    remind_hour             BOOLEAN NOT NULL DEFAULT true,
    reschedule_needs_parent BOOLEAN NOT NULL DEFAULT true, -- для ученика: перенос/отмена через подтверждение родителя
    onboarded               BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE parent_links (
    parent_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    student_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (parent_id, student_id)
);

CREATE TABLE invites (
    code       TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('parent_link', 'child_link', 'tutor')), -- parent_link: ученик зовёт родителя; child_link: родитель зовёт ребёнка
    created_by BIGINT NOT NULL REFERENCES users(id),
    target_id  BIGINT,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);

-- Диалоговое состояние (ожидаем текст: имя, причину отмены и т.п.)
CREATE TABLE user_states (
    user_id    BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    state      TEXT  NOT NULL,
    data       JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subjects (
    id   SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE tutors (
    user_id        BIGINT  PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    bio            TEXT    NOT NULL DEFAULT '',
    lesson_minutes INT     NOT NULL DEFAULT 60 CHECK (lesson_minutes BETWEEN 15 AND 240),
    price_override BIGINT  CHECK (price_override >= 0), -- копейки за урок; NULL = общая цена
    active         BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE tutor_subjects (
    tutor_id   BIGINT NOT NULL REFERENCES tutors(user_id) ON DELETE CASCADE,
    subject_id INT    NOT NULL REFERENCES subjects(id) ON DELETE CASCADE,
    PRIMARY KEY (tutor_id, subject_id)
);

-- Недельный шаблон окон в часовом поясе репетитора. weekday: 0=вс ... 6=сб (как time.Weekday)
CREATE TABLE tutor_windows (
    id        BIGSERIAL PRIMARY KEY,
    tutor_id  BIGINT   NOT NULL REFERENCES tutors(user_id) ON DELETE CASCADE,
    weekday   SMALLINT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_min INT      NOT NULL CHECK (start_min BETWEEN 0 AND 1440),
    end_min   INT      NOT NULL CHECK (end_min BETWEEN 0 AND 1440),
    CHECK (end_min > start_min)
);
CREATE INDEX tutor_windows_tutor ON tutor_windows(tutor_id);

-- Исключения: отпуск, сессия (даты включительно, в поясе репетитора)
CREATE TABLE tutor_exceptions (
    id        BIGSERIAL PRIMARY KEY,
    tutor_id  BIGINT NOT NULL REFERENCES tutors(user_id) ON DELETE CASCADE,
    from_date DATE   NOT NULL,
    to_date   DATE   NOT NULL,
    note      TEXT   NOT NULL DEFAULT '',
    CHECK (to_date >= from_date)
);

-- Ученик выбрал репетитора по предмету (после понравившегося пробного)
CREATE TABLE enrollments (
    id         BIGSERIAL PRIMARY KEY,
    student_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tutor_id   BIGINT NOT NULL REFERENCES tutors(user_id),
    subject_id INT    NOT NULL REFERENCES subjects(id),
    active     BOOLEAN NOT NULL DEFAULT true,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX enrollments_one_active ON enrollments(student_id, subject_id) WHERE active;

-- Постоянный слот: каждую неделю в weekday/start_min по поясу репетитора
CREATE TABLE recurring_slots (
    id           BIGSERIAL PRIMARY KEY,
    student_id   BIGINT   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tutor_id     BIGINT   NOT NULL REFERENCES tutors(user_id),
    subject_id   INT      NOT NULL REFERENCES subjects(id),
    weekday      SMALLINT NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_min    INT      NOT NULL,
    duration_min INT      NOT NULL,
    active       BOOLEAN  NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE lessons (
    id                BIGSERIAL PRIMARY KEY,
    student_id        BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tutor_id          BIGINT      NOT NULL REFERENCES tutors(user_id),
    subject_id        INT         NOT NULL REFERENCES subjects(id),
    starts_at         TIMESTAMPTZ NOT NULL,
    duration_min      INT         NOT NULL,
    kind              TEXT        NOT NULL CHECK (kind IN ('trial', 'regular')),
    status            TEXT        NOT NULL DEFAULT 'scheduled'
                                  CHECK (status IN ('scheduled', 'held', 'no_show', 'cancelled')),
    recurring_slot_id BIGINT      REFERENCES recurring_slots(id),
    cancelled_by      BIGINT      REFERENCES users(id),
    cancel_reason     TEXT        NOT NULL DEFAULT '',
    late_cancel       BOOLEAN     NOT NULL DEFAULT false,
    mark_prompted     BOOLEAN     NOT NULL DEFAULT false,
    marked_auto       BOOLEAN     NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX lessons_tutor_time   ON lessons(tutor_id, starts_at) WHERE status = 'scheduled';
CREATE INDEX lessons_student_time ON lessons(student_id, starts_at);
-- Один пробный на пару ученик–репетитор (отменённый пробный можно взять заново)
CREATE UNIQUE INDEX lessons_one_trial ON lessons(student_id, tutor_id) WHERE kind = 'trial' AND status <> 'cancelled';
-- Повторяющийся слот не порождает дубли на одну дату
CREATE UNIQUE INDEX lessons_recurring_once ON lessons(recurring_slot_id, starts_at) WHERE recurring_slot_id IS NOT NULL;

-- Запрос ученика на перенос/отмену, ждущий подтверждения родителя
CREATE TABLE change_requests (
    id            BIGSERIAL PRIMARY KEY,
    lesson_id     BIGINT NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    requested_by  BIGINT NOT NULL REFERENCES users(id),
    kind          TEXT   NOT NULL CHECK (kind IN ('reschedule', 'cancel')),
    new_starts_at TIMESTAMPTZ,
    reason        TEXT   NOT NULL DEFAULT '',
    status        TEXT   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'declined')),
    decided_by    BIGINT REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE trial_feedback (
    id           BIGSERIAL PRIMARY KEY,
    lesson_id    BIGINT  NOT NULL UNIQUE REFERENCES lessons(id) ON DELETE CASCADE,
    student_id   BIGINT  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tutor_id     BIGINT  NOT NULL REFERENCES tutors(user_id),
    liked        BOOLEAN NOT NULL,
    comment      TEXT    NOT NULL DEFAULT '',
    forwarded_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Ключ-значение для общих настроек школы (цена урока и т.п.)
CREATE TABLE app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO app_settings(key, value) VALUES ('lesson_price', '150000'); -- 1500 ₽ в копейках

CREATE TABLE products (
    id           SERIAL PRIMARY KEY,
    name         TEXT    NOT NULL,
    kind         TEXT    NOT NULL CHECK (kind IN ('single', 'pack', 'subscription')),
    lessons      INT     NOT NULL CHECK (lessons > 0),
    discount_pct INT     NOT NULL DEFAULT 0 CHECK (discount_pct BETWEEN 0 AND 100),
    valid_days   INT     CHECK (valid_days > 0), -- для абонемента
    active       BOOLEAN NOT NULL DEFAULT true
);
INSERT INTO products(name, kind, lessons, discount_pct, valid_days) VALUES
    ('Разовый урок', 'single', 1, 0, NULL),
    ('Пакет 8 уроков', 'pack', 8, 5, NULL),
    ('Абонемент на месяц (8 уроков)', 'subscription', 8, 10, 31);

-- Заглушка платежа: запись без реального списания денег
CREATE TABLE payments (
    id         BIGSERIAL PRIMARY KEY,
    payer_id   BIGINT NOT NULL REFERENCES users(id),
    student_id BIGINT NOT NULL REFERENCES users(id),
    tutor_id   BIGINT NOT NULL REFERENCES tutors(user_id),
    product_id INT    NOT NULL REFERENCES products(id),
    amount     BIGINT NOT NULL, -- копейки
    status     TEXT   NOT NULL DEFAULT 'stub_paid',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Журнал операций с уроками (append-only). Баланс = сумма delta.
-- unit_price фиксирует цену на момент покупки: изменение цены не трогает купленное.
CREATE TABLE ledger (
    id         BIGSERIAL PRIMARY KEY,
    student_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tutor_id   BIGINT NOT NULL REFERENCES tutors(user_id),
    delta      INT    NOT NULL,
    unit_price BIGINT NOT NULL DEFAULT 0,
    reason     TEXT   NOT NULL CHECK (reason IN ('purchase', 'lesson', 'refund', 'adjust')),
    payment_id BIGINT REFERENCES payments(id),
    lesson_id  BIGINT REFERENCES lessons(id),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ledger_pair ON ledger(student_id, tutor_id);
CREATE UNIQUE INDEX ledger_lesson_once ON ledger(lesson_id) WHERE reason = 'lesson';

CREATE TABLE channel_access (
    student_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    active     BOOLEAN NOT NULL DEFAULT true,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

-- Какие напоминания уже отправлены (по получателю, т.к. у всех свой пояс)
CREATE TABLE reminder_log (
    lesson_id BIGINT NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
    user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind      TEXT   NOT NULL,
    sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (lesson_id, user_id, kind)
);

INSERT INTO subjects(name) VALUES ('Математика'), ('Физика');
