package store

const monitorSchema = `
CREATE TABLE monitors(id INTEGER PRIMARY KEY, config TEXT NOT NULL, heartbeat_hash TEXT NOT NULL DEFAULT '', heartbeat_at INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX heartbeat_token ON monitors(heartbeat_hash) WHERE heartbeat_hash<>'';
CREATE TABLE monitor_state(monitor_id INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,status TEXT NOT NULL DEFAULT 'pending', checked INTEGER NOT NULL DEFAULT 0,latency REAL NOT NULL DEFAULT 0,failures INTEGER NOT NULL DEFAULT 0,message TEXT NOT NULL DEFAULT '');
CREATE TABLE samples(monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,at INTEGER NOT NULL,ok INTEGER NOT NULL,latency REAL NOT NULL);
CREATE INDEX samples_time ON samples(at);
CREATE INDEX samples_monitor ON samples(monitor_id,at);
CREATE TABLE aggregates(monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,bucket INTEGER NOT NULL,resolution INTEGER NOT NULL,total INTEGER NOT NULL,up INTEGER NOT NULL,latency_sum REAL NOT NULL,PRIMARY KEY(monitor_id,bucket,resolution));
CREATE INDEX aggregate_time ON aggregates(resolution,bucket);
CREATE TABLE incidents(id INTEGER PRIMARY KEY,monitor_id INTEGER REFERENCES monitors(id) ON DELETE SET NULL,name TEXT NOT NULL,opened INTEGER NOT NULL,closed INTEGER,message TEXT NOT NULL);
CREATE UNIQUE INDEX one_open_incident ON incidents(monitor_id) WHERE closed IS NULL;
CREATE TABLE incident_events(id INTEGER PRIMARY KEY,incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,at INTEGER NOT NULL,kind TEXT NOT NULL,message TEXT NOT NULL);
PRAGMA user_version=2;`
