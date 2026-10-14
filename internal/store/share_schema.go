package store

const shareSchema = `
ALTER TABLE transfers ADD COLUMN share_id TEXT NOT NULL DEFAULT '';
CREATE TABLE shares(id TEXT PRIMARY KEY,token_hash TEXT NOT NULL UNIQUE,owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,path TEXT NOT NULL,password_hash TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL,expires INTEGER NOT NULL,max_downloads INTEGER NOT NULL DEFAULT 0,downloads INTEGER NOT NULL DEFAULT 0,revoked INTEGER NOT NULL DEFAULT 0);
CREATE INDEX share_owner ON shares(owner_id,created);
CREATE TABLE share_attempts(ip_hash TEXT PRIMARY KEY,attempts INTEGER NOT NULL,window_start INTEGER NOT NULL);
PRAGMA user_version=6;`
