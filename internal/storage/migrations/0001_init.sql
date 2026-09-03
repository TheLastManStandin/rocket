CREATE TABLE IF NOT EXISTS users (
    id         BIGSERIAL PRIMARY KEY,
    tg_id      BIGINT      NOT NULL UNIQUE,
    username   TEXT        NOT NULL DEFAULT '',
    first_name TEXT        NOT NULL DEFAULT '',
    photo_url  TEXT        NOT NULL DEFAULT '',
    balance    BIGINT      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rounds (
    id          BIGSERIAL     PRIMARY KEY,
    user_id     BIGINT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    crash_point NUMERIC(12,2) NOT NULL,
    started_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    crashed_at  TIMESTAMPTZ,
    status      TEXT          NOT NULL DEFAULT 'betting'
);
CREATE INDEX IF NOT EXISTS rounds_user_started_idx ON rounds (user_id, started_at DESC);

-- Соло-режим: на раунд приходится не больше одной ставки живого игрока.
CREATE TABLE IF NOT EXISTS bets (
    id                 BIGSERIAL     PRIMARY KEY,
    round_id           BIGINT        NOT NULL UNIQUE REFERENCES rounds(id) ON DELETE CASCADE,
    user_id            BIGINT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount             BIGINT        NOT NULL CHECK (amount > 0),
    cashout_multiplier NUMERIC(12,2),
    payout             BIGINT        NOT NULL DEFAULT 0,
    status             TEXT          NOT NULL DEFAULT 'active',
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    settled_at         TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS bets_user_created_idx ON bets (user_id, created_at DESC);

-- Журнал движения баланса: сумма delta по пользователю обязана сходиться с users.balance.
CREATE TABLE IF NOT EXISTS ledger (
    id         BIGSERIAL   PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delta      BIGINT      NOT NULL,
    reason     TEXT        NOT NULL,
    ref_id     TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ledger_user_created_idx ON ledger (user_id, created_at DESC);
