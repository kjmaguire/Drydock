-- +goose Up
create table runs(id integer primary key, name text not null);
create virtual table run_fts using fts5(name, content='runs', content_rowid='id');
-- +goose StatementBegin
create trigger runs_ai after insert on runs begin
  insert into run_fts(rowid, name) values (new.id, new.name);
end;
-- +goose StatementEnd
-- +goose Down
drop trigger runs_ai;
drop table run_fts;
drop table runs;
