-- Отменяет соло-режим из 0001: раунд теперь один на всех, а не свой у каждого.
-- У раунда больше нет владельца, и ставок на нём может быть много.
ALTER TABLE rounds ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE bets DROP CONSTRAINT IF EXISTS bets_round_id_key;
ALTER TABLE bets ADD CONSTRAINT bets_round_user_key UNIQUE (round_id, user_id);

-- Полоса истории общая, и читается по раундам без владельца.
CREATE INDEX IF NOT EXISTS rounds_shared_idx ON rounds (id DESC) WHERE user_id IS NULL;
