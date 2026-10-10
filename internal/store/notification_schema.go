package store

const notificationSchema = `
CREATE TABLE notification_channels(id INTEGER PRIMARY KEY,config TEXT NOT NULL);
CREATE TABLE notification_queue(id INTEGER PRIMARY KEY,channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,event_id INTEGER NOT NULL,kind TEXT NOT NULL,payload TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL,created_at INTEGER NOT NULL,delivered_at INTEGER,last_error TEXT NOT NULL DEFAULT '',UNIQUE(channel_id,event_id,kind));
CREATE INDEX delivery_due ON notification_queue(status,next_at);
CREATE TABLE notification_cooldown(channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,monitor_id INTEGER NOT NULL,kind TEXT NOT NULL,last_at INTEGER NOT NULL,PRIMARY KEY(channel_id,monitor_id,kind));
CREATE TABLE external_heartbeat(id INTEGER PRIMARY KEY CHECK(id=1),enabled INTEGER NOT NULL DEFAULT 0,url_env TEXT NOT NULL DEFAULT '',last_attempt INTEGER NOT NULL DEFAULT 0,last_success INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '');
INSERT INTO external_heartbeat(id) VALUES(1);
PRAGMA user_version=3;`
