CREATE TABLE IF NOT EXISTS install_product (
    "only" boolean PRIMARY KEY DEFAULT true CHECK ("only"),
    product text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    release text NOT NULL DEFAULT '',
    release_at timestamptz
);
