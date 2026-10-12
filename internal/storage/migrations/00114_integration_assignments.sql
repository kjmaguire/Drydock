-- +goose Up
-- Integrations are connected once, at the workspace level, and assigned to projects (decision P42, docs/integrations.md
-- "As built: workspace connections"). Before this migration a connection carried one project and one remote scope; now a
-- connection carries the credential and the account it reaches, and each assignment carries a project and that project's scope
-- (Linear team, Notion data source, Sentry organization and project, Aikido repository, GitHub repository).
--
-- Nothing is lost: every connection that had a project gets an assignment with the same id suffix ('asg_' || connection id),
-- its config, its cursor (scope 'default' becomes the assignment id) and its items. A connection without a project keeps its
-- items (assignment_id '') and its config_json, which the first assignment adopts. Credentials are not touched: they stay in
-- the credential store under the same reference. GitHub joins the kinds; it never has a credential (the user's own gh).

CREATE TABLE integrations_new (
  id              TEXT PRIMARY KEY,
  kind            TEXT NOT NULL CHECK (kind IN ('linear','notion','sentry','aikido','github')),
  name            TEXT NOT NULL DEFAULT '',        -- the vendor's name; fixed, not editable
  project_id      TEXT NOT NULL DEFAULT '',        -- legacy (before P42); new connections leave it empty
  status          TEXT NOT NULL DEFAULT 'untested',
  status_detail   TEXT NOT NULL DEFAULT '',
  auth_kind       TEXT NOT NULL DEFAULT 'api_key', -- api_key | oauth | client_credentials | gh
  credential_ref  TEXT NOT NULL DEFAULT '',        -- a reference into the credential store, never a secret
  base_url        TEXT NOT NULL DEFAULT '',        -- API base override (self-hosted Sentry, Aikido region)
  config_json     TEXT NOT NULL DEFAULT '{}',      -- legacy remote scope, adopted by the first assignment
  poll_interval_s INTEGER NOT NULL DEFAULT 0,
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  version         INTEGER NOT NULL DEFAULT 1,
  account_json    TEXT NOT NULL DEFAULT '{}',      -- what the vendor's API says the credential reaches: account, workspace, scopes
  checked_at      INTEGER NOT NULL DEFAULT 0       -- when that was last read
);
INSERT INTO integrations_new(id,kind,name,project_id,status,status_detail,auth_kind,credential_ref,base_url,config_json,poll_interval_s,enabled,created_at,updated_at,version)
  SELECT id,kind,
         CASE kind WHEN 'linear' THEN 'Linear' WHEN 'notion' THEN 'Notion' WHEN 'sentry' THEN 'Sentry' ELSE 'Aikido' END,
         project_id,
         -- the app now calls the vendor's API instead of its MCP server: every connection is tested again
         'untested', '',
         auth_kind, credential_ref,
         -- an MCP endpoint override means nothing to the API client; a self-hosted Sentry keeps its host
         CASE WHEN kind = 'sentry' AND base_url NOT LIKE '%/mcp%' AND base_url NOT LIKE '%/sse%' THEN base_url ELSE '' END,
         json_remove(config_json, '$.argv'),
         poll_interval_s,enabled,created_at,updated_at,version
  FROM integrations;
DROP TABLE integrations;
ALTER TABLE integrations_new RENAME TO integrations;
CREATE INDEX integrations_kind ON integrations(kind);

CREATE TABLE integration_assignments (
  id             TEXT PRIMARY KEY,
  integration_id TEXT NOT NULL,
  project_id     TEXT NOT NULL,
  config_json    TEXT NOT NULL DEFAULT '{}',        -- this project's remote scope
  enabled        INTEGER NOT NULL DEFAULT 1,
  created_by     TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  version        INTEGER NOT NULL DEFAULT 1,
  UNIQUE (integration_id, project_id)
);
INSERT INTO integration_assignments(id,integration_id,project_id,config_json,enabled,created_by,created_at,updated_at,version)
  SELECT 'asg_' || id, id, project_id, config_json, 1, 'migration', created_at, updated_at, 1 FROM integrations WHERE project_id <> '';

-- the cursor of a migrated connection becomes its assignment's cursor
UPDATE integration_cursors SET scope = 'asg_' || integration_id
  WHERE scope = 'default' AND integration_id IN (SELECT integration_id FROM integration_assignments WHERE id = 'asg_' || integration_id);

-- external items belong to an assignment: one connection assigned to two projects keeps each project's items apart
CREATE TABLE external_items_new (
  id                 TEXT PRIMARY KEY,
  integration_id     TEXT NOT NULL,
  assignment_id      TEXT NOT NULL DEFAULT '',
  source             TEXT NOT NULL,
  project_id         TEXT NOT NULL DEFAULT '',
  external_id        TEXT NOT NULL,
  kind               TEXT NOT NULL CHECK (kind IN ('issue','page','pr','error_event','finding')),
  issue_ref          TEXT NOT NULL DEFAULT '',
  url                TEXT NOT NULL DEFAULT '',
  title              TEXT NOT NULL DEFAULT '',
  excerpt            TEXT NOT NULL DEFAULT '',
  body_ref           TEXT NOT NULL DEFAULT '',
  state              TEXT NOT NULL DEFAULT '',
  labels_json        TEXT NOT NULL DEFAULT '[]',
  assignee           TEXT NOT NULL DEFAULT '',
  priority           TEXT NOT NULL DEFAULT '',
  parent_external_id TEXT NOT NULL DEFAULT '',
  meta_json          TEXT NOT NULL DEFAULT '{}',
  external_created_at INTEGER NOT NULL DEFAULT 0,
  external_updated_at INTEGER NOT NULL DEFAULT 0,
  etag               TEXT NOT NULL DEFAULT '',
  content_hash       TEXT NOT NULL,
  fetched_at         INTEGER NOT NULL,
  risk               INTEGER NOT NULL DEFAULT 0,
  risk_flags         TEXT NOT NULL DEFAULT '[]',
  redactions         INTEGER NOT NULL DEFAULT 0,
  truncated          INTEGER NOT NULL DEFAULT 0,
  untrusted          INTEGER NOT NULL DEFAULT 1 CHECK (untrusted = 1),
  task_id            TEXT NOT NULL DEFAULT '',
  imported_by        TEXT NOT NULL DEFAULT '',
  imported_at        INTEGER NOT NULL DEFAULT 0,
  gone               INTEGER NOT NULL DEFAULT 0,
  version            INTEGER NOT NULL DEFAULT 1,
  UNIQUE (integration_id, assignment_id, external_id)
);
INSERT INTO external_items_new(id,integration_id,assignment_id,source,project_id,external_id,kind,issue_ref,url,title,excerpt,body_ref,state,labels_json,assignee,priority,
    parent_external_id,meta_json,external_created_at,external_updated_at,etag,content_hash,fetched_at,risk,risk_flags,redactions,truncated,untrusted,task_id,imported_by,imported_at,gone,version)
  SELECT e.id,e.integration_id,
         CASE WHEN EXISTS (SELECT 1 FROM integration_assignments a WHERE a.id = 'asg_' || e.integration_id) THEN 'asg_' || e.integration_id ELSE '' END,
         e.source,e.project_id,e.external_id,e.kind,e.issue_ref,e.url,e.title,e.excerpt,e.body_ref,e.state,e.labels_json,e.assignee,e.priority,
         e.parent_external_id,e.meta_json,e.external_created_at,e.external_updated_at,e.etag,e.content_hash,e.fetched_at,e.risk,e.risk_flags,e.redactions,e.truncated,e.untrusted,
         e.task_id,e.imported_by,e.imported_at,e.gone,e.version
  FROM external_items e;
DROP INDEX external_items_integration;
DROP INDEX external_items_task;
DROP INDEX external_items_rule;
DROP TABLE external_items;
ALTER TABLE external_items_new RENAME TO external_items;
CREATE INDEX external_items_integration ON external_items(integration_id, external_updated_at DESC);
CREATE INDEX external_items_assignment ON external_items(assignment_id, external_updated_at DESC);
CREATE INDEX external_items_task ON external_items(task_id) WHERE task_id <> '';
CREATE INDEX external_items_rule ON external_items(imported_by, imported_at) WHERE imported_by <> '';

-- Outbound writes proposed before this migration name an MCP tool. Drydock now writes through the vendor's API, so a proposal
-- that was not sent yet cannot be sent as approved: it is cancelled (with its approval) and has to be proposed again. Sent and
-- failed rows stay as history.
UPDATE approvals SET status = 'CANCELLED'
  WHERE status IN ('REQUESTED','APPROVED')
    AND id IN (SELECT approval_id FROM integration_outbox WHERE status IN ('planned','awaiting_approval','approved') AND approval_id <> '');
UPDATE integration_outbox
  SET status = 'cancelled', version = version + 1,
      error = 'Drydock now writes to the vendor''s API directly instead of through its MCP server; propose this action again.'
  WHERE status IN ('planned','awaiting_approval','approved');

-- +goose Down
-- Back to one project per connection: a connection keeps its first assignment's project and scope; items of other assignments
-- that repeat an external id are dropped (the older schema keyed items by connection); GitHub rows go (the older CHECK).
CREATE TABLE integrations_old (
  id              TEXT PRIMARY KEY,
  kind            TEXT NOT NULL CHECK (kind IN ('linear','notion','sentry','aikido')),
  name            TEXT NOT NULL DEFAULT '',
  project_id      TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'untested',
  status_detail   TEXT NOT NULL DEFAULT '',
  auth_kind       TEXT NOT NULL DEFAULT 'api_key',
  credential_ref  TEXT NOT NULL DEFAULT '',
  base_url        TEXT NOT NULL DEFAULT '',
  config_json     TEXT NOT NULL DEFAULT '{}',
  poll_interval_s INTEGER NOT NULL DEFAULT 0,
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  version         INTEGER NOT NULL DEFAULT 1
);
INSERT INTO integrations_old(id,kind,name,project_id,status,status_detail,auth_kind,credential_ref,base_url,config_json,poll_interval_s,enabled,created_at,updated_at,version)
  SELECT i.id,i.kind,i.name,
         COALESCE((SELECT a.project_id FROM integration_assignments a WHERE a.integration_id = i.id ORDER BY a.created_at, a.id LIMIT 1), i.project_id),
         i.status,i.status_detail,i.auth_kind,i.credential_ref,i.base_url,
         COALESCE((SELECT a.config_json FROM integration_assignments a WHERE a.integration_id = i.id ORDER BY a.created_at, a.id LIMIT 1), i.config_json),
         i.poll_interval_s,i.enabled,i.created_at,i.updated_at,i.version
  FROM integrations i WHERE i.kind <> 'github';
DROP TABLE integrations;
ALTER TABLE integrations_old RENAME TO integrations;
CREATE INDEX integrations_kind ON integrations(kind);

CREATE TABLE external_items_old (
  id                 TEXT PRIMARY KEY,
  integration_id     TEXT NOT NULL,
  source             TEXT NOT NULL,
  project_id         TEXT NOT NULL DEFAULT '',
  external_id        TEXT NOT NULL,
  kind               TEXT NOT NULL CHECK (kind IN ('issue','page','pr','error_event','finding')),
  issue_ref          TEXT NOT NULL DEFAULT '',
  url                TEXT NOT NULL DEFAULT '',
  title              TEXT NOT NULL DEFAULT '',
  excerpt            TEXT NOT NULL DEFAULT '',
  body_ref           TEXT NOT NULL DEFAULT '',
  state              TEXT NOT NULL DEFAULT '',
  labels_json        TEXT NOT NULL DEFAULT '[]',
  assignee           TEXT NOT NULL DEFAULT '',
  priority           TEXT NOT NULL DEFAULT '',
  parent_external_id TEXT NOT NULL DEFAULT '',
  meta_json          TEXT NOT NULL DEFAULT '{}',
  external_created_at INTEGER NOT NULL DEFAULT 0,
  external_updated_at INTEGER NOT NULL DEFAULT 0,
  etag               TEXT NOT NULL DEFAULT '',
  content_hash       TEXT NOT NULL,
  fetched_at         INTEGER NOT NULL,
  risk               INTEGER NOT NULL DEFAULT 0,
  risk_flags         TEXT NOT NULL DEFAULT '[]',
  redactions         INTEGER NOT NULL DEFAULT 0,
  truncated          INTEGER NOT NULL DEFAULT 0,
  untrusted          INTEGER NOT NULL DEFAULT 1 CHECK (untrusted = 1),
  task_id            TEXT NOT NULL DEFAULT '',
  imported_by        TEXT NOT NULL DEFAULT '',
  imported_at        INTEGER NOT NULL DEFAULT 0,
  gone               INTEGER NOT NULL DEFAULT 0,
  version            INTEGER NOT NULL DEFAULT 1,
  UNIQUE (integration_id, external_id)
);
INSERT OR IGNORE INTO external_items_old(id,integration_id,source,project_id,external_id,kind,issue_ref,url,title,excerpt,body_ref,state,labels_json,assignee,priority,
    parent_external_id,meta_json,external_created_at,external_updated_at,etag,content_hash,fetched_at,risk,risk_flags,redactions,truncated,untrusted,task_id,imported_by,imported_at,gone,version)
  SELECT id,integration_id,source,project_id,external_id,kind,issue_ref,url,title,excerpt,body_ref,state,labels_json,assignee,priority,
    parent_external_id,meta_json,external_created_at,external_updated_at,etag,content_hash,fetched_at,risk,risk_flags,redactions,truncated,untrusted,task_id,imported_by,imported_at,gone,version
  FROM external_items WHERE source <> 'github' ORDER BY task_id DESC, fetched_at DESC;
DROP INDEX external_items_assignment;
DROP INDEX external_items_integration;
DROP INDEX external_items_task;
DROP INDEX external_items_rule;
DROP TABLE external_items;
ALTER TABLE external_items_old RENAME TO external_items;
CREATE INDEX external_items_integration ON external_items(integration_id, external_updated_at DESC);
CREATE INDEX external_items_task ON external_items(task_id) WHERE task_id <> '';
CREATE INDEX external_items_rule ON external_items(imported_by, imported_at) WHERE imported_by <> '';

UPDATE integration_cursors SET scope = 'default'
  WHERE scope = (SELECT a.id FROM integration_assignments a WHERE a.integration_id = integration_cursors.integration_id ORDER BY a.created_at, a.id LIMIT 1);
DELETE FROM integration_cursors WHERE scope <> 'default';
DROP TABLE integration_assignments;
