-- Opening balances used to be granted straight into users.balance, so early
-- accounts carry an amount the ledger cannot explain. Book the difference as
-- the welcome grant it always was, and every player reconciles again.
INSERT INTO ledger (user_id, delta, reason, ref_id)
SELECT u.id, u.balance - COALESCE(SUM(l.delta), 0), 'welcome', 'backfill'
FROM users u
LEFT JOIN ledger l ON l.user_id = u.id
GROUP BY u.id, u.balance
HAVING u.balance - COALESCE(SUM(l.delta), 0) <> 0;
