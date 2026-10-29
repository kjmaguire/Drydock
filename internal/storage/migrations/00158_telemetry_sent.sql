-- +goose Up
-- When the OTLP exporter delivered a span or a metric (decision P121): NULL is never sent. It lets "Also send what was recorded
-- before" pick only rows no collector has had, and makes the local rows the exact log of what left. Since P121 a span's attrs
-- can also hold the OpenTelemetry GenAI content attributes (prompts and completions, redacted), but only while a person has
-- turned the separate content export on; without it they still hold identifier-shaped values only.
ALTER TABLE telemetry_spans ADD COLUMN otlp_sent_at INTEGER;
ALTER TABLE telemetry_metrics ADD COLUMN otlp_sent_at INTEGER;

-- +goose Down
ALTER TABLE telemetry_metrics DROP COLUMN otlp_sent_at;
ALTER TABLE telemetry_spans DROP COLUMN otlp_sent_at;
