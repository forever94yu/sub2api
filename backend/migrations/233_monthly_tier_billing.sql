-- 仅新增结构；默认关闭，不修改既有余额、消费或分组倍率。
ALTER TABLE groups ADD COLUMN IF NOT EXISTS monthly_tier_policy JSONB;

CREATE TABLE IF NOT EXISTS monthly_tier_usage (
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    month VARCHAR(7) NOT NULL,
    raw_cost NUMERIC(20,10) NOT NULL DEFAULT 0 CHECK (raw_cost >= 0),
    charged_cost NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (charged_cost >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id, month)
);

CREATE TABLE IF NOT EXISTS monthly_tier_settlements (
    request_id VARCHAR(255) NOT NULL,
    api_key_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    month VARCHAR(7) NOT NULL,
    raw_cost NUMERIC(20,10) NOT NULL CHECK (raw_cost >= 0),
    charged_cost NUMERIC(20,8) NOT NULL CHECK (charged_cost >= 0),
    raw_before NUMERIC(20,10) NOT NULL,
    raw_after NUMERIC(20,10) NOT NULL,
    policy JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (request_id, api_key_id)
);
CREATE INDEX IF NOT EXISTS monthly_tier_settlements_user_month
    ON monthly_tier_settlements (user_id, group_id, month);
