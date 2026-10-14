-- Optional 5-hour subscription quota. Existing subscriptions start their first
-- 5-hour window on the next request.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS five_hour_limit_usd DECIMAL(20,8);
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS five_hour_window_start TIMESTAMPTZ;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS five_hour_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0;
