-- +goose Up
-- A task's labels (decision P97): short lower-case words a person sorts tasks by ("auth", "flaky-test"), a JSON array of strings
-- like files and acceptance. tasks.NormalizeLabels bounds them: at most MaxLabels, each at most MaxLabelLen characters.
ALTER TABLE tasks ADD COLUMN labels TEXT NOT NULL DEFAULT '[]';
-- task.summaries reads the reviews of many tasks at once
CREATE INDEX task_reviews_task ON task_reviews(task_id, id);

-- +goose Down
DROP INDEX task_reviews_task;
ALTER TABLE tasks DROP COLUMN labels;
