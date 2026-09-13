CREATE TABLE push_subscriptions (
                                    id          uuid PRIMARY KEY,
                                    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                                    endpoint    text NOT NULL,
                                    p256dh      text NOT NULL,
                                    auth        text NOT NULL,
                                    user_agent  text NOT NULL DEFAULT '',
                                    created_at  timestamptz NOT NULL DEFAULT now(),
                                    last_ok_at  timestamptz,
                                    failures    integer NOT NULL DEFAULT 0,
                                    CONSTRAINT push_subscriptions_endpoint_key UNIQUE (endpoint),
                                    CONSTRAINT push_subscriptions_endpoint_check CHECK (endpoint LIKE 'https://%')
);

CREATE INDEX push_subscriptions_user_id_idx ON push_subscriptions (user_id);