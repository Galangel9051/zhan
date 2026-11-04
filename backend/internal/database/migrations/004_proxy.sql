CREATE TABLE proxy_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 action TEXT NOT NULL,
 host_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'running',
 message TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 finished_at TEXT NOT NULL DEFAULT ''
);
