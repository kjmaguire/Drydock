-- +goose Up
-- Mappings of an assignment (decision P114, docs/integrations.md "As built: webhooks, mappings, status sync"): what a vendor's
-- values mean in Drydock, chosen by a person. scope 'status' maps one of the vendor's states (remote_id, as the vendor names it:
-- a Linear workflow state, a Notion status option, Sentry's resolved/unresolved) to a Drydock task status (local) and says
-- which way it is used: to_vendor (a task entering that status proposes the vendor write), from_vendor (the vendor's state is
-- offered to the person as a change to the task), or both. scope 'property' maps a Drydock field (remote_id: state, priority,
-- assignee, labels) to the Notion property (local) of a data source's typed rows that fills it.
CREATE TABLE integration_mappings (
  assignment_id TEXT NOT NULL,
  scope         TEXT NOT NULL CHECK (scope IN ('status','property')),
  remote_id     TEXT NOT NULL,
  remote_name   TEXT NOT NULL DEFAULT '',
  local         TEXT NOT NULL,
  direction     TEXT NOT NULL DEFAULT 'both' CHECK (direction IN ('to_vendor','from_vendor','both')),
  created_by    TEXT NOT NULL,
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (assignment_id, scope, remote_id)
);

-- Rules gain the link_task action: a matching item is linked to an existing task (the one the rule names, or the project's
-- task whose issue reference is the item's) instead of creating one. SQLite cannot change a CHECK, so the table is rebuilt.
CREATE TABLE integration_rules_new (
  id             TEXT PRIMARY KEY,
  integration_id TEXT NOT NULL,
  project_id     TEXT NOT NULL,
  name           TEXT NOT NULL,
  when_cel       TEXT NOT NULL,
  action         TEXT NOT NULL CHECK (action IN ('create_task','link_task','notify')),
  params_json    TEXT NOT NULL DEFAULT '{}',
  max_per_day    INTEGER NOT NULL DEFAULT 5,
  dry_run        INTEGER NOT NULL DEFAULT 1,
  enabled        INTEGER NOT NULL DEFAULT 0,
  created_by     TEXT NOT NULL,
  updated_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  version        INTEGER NOT NULL DEFAULT 1
);
INSERT INTO integration_rules_new(id,integration_id,project_id,name,when_cel,action,params_json,max_per_day,dry_run,enabled,created_by,updated_by,created_at,updated_at,version)
  SELECT id,integration_id,project_id,name,when_cel,action,params_json,max_per_day,dry_run,enabled,created_by,updated_by,created_at,updated_at,version FROM integration_rules;
DROP INDEX integration_rules_integration;
DROP TABLE integration_rules;
ALTER TABLE integration_rules_new RENAME TO integration_rules;
CREATE INDEX integration_rules_integration ON integration_rules(integration_id);

-- +goose Down
-- link_task rules cannot exist under the older CHECK: they are removed with their matches.
DELETE FROM integration_rule_matches WHERE rule_id IN (SELECT id FROM integration_rules WHERE action = 'link_task');
CREATE TABLE integration_rules_old (
  id             TEXT PRIMARY KEY,
  integration_id TEXT NOT NULL,
  project_id     TEXT NOT NULL,
  name           TEXT NOT NULL,
  when_cel       TEXT NOT NULL,
  action         TEXT NOT NULL CHECK (action IN ('create_task','notify')),
  params_json    TEXT NOT NULL DEFAULT '{}',
  max_per_day    INTEGER NOT NULL DEFAULT 5,
  dry_run        INTEGER NOT NULL DEFAULT 1,
  enabled        INTEGER NOT NULL DEFAULT 0,
  created_by     TEXT NOT NULL,
  updated_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  version        INTEGER NOT NULL DEFAULT 1
);
INSERT INTO integration_rules_old(id,integration_id,project_id,name,when_cel,action,params_json,max_per_day,dry_run,enabled,created_by,updated_by,created_at,updated_at,version)
  SELECT id,integration_id,project_id,name,when_cel,action,params_json,max_per_day,dry_run,enabled,created_by,updated_by,created_at,updated_at,version
  FROM integration_rules WHERE action <> 'link_task';
DROP INDEX integration_rules_integration;
DROP TABLE integration_rules;
ALTER TABLE integration_rules_old RENAME TO integration_rules;
CREATE INDEX integration_rules_integration ON integration_rules(integration_id);
DROP TABLE integration_mappings;
