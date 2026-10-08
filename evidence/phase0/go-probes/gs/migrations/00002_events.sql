-- +goose Up
create table events(id integer primary key, run_id integer references runs(id), payload text);
-- +goose Down
drop table events;
