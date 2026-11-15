package store

// Payloads use master.key authenticated encryption; bearer credentials are hashed.
func (s *Store) initConnectivity() error {
	_, err := s.DB.Exec(`
CREATE TABLE IF NOT EXISTS connectivity_probes(id TEXT PRIMARY KEY,name TEXT NOT NULL,role TEXT NOT NULL,expected_ip TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,token_hash TEXT NOT NULL DEFAULT '',last_seen INTEGER NOT NULL DEFAULT 0,version TEXT NOT NULL DEFAULT '',egress BLOB NOT NULL DEFAULT '{}',checks INTEGER NOT NULL DEFAULT 0,successes INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS probe_enrollments(token_hash TEXT PRIMARY KEY,probe_id TEXT NOT NULL REFERENCES connectivity_probes(id),expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS screenings(id TEXT PRIMARY KEY,job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id),state TEXT NOT NULL,stage TEXT NOT NULL,payload BLOB NOT NULL,data BLOB NOT NULL,stop INTEGER NOT NULL DEFAULT 0,busy INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,updated INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS screenings_dispatch ON screenings(state,busy,created);
CREATE TABLE IF NOT EXISTS probe_tasks(id TEXT PRIMARY KEY,probe_id TEXT NOT NULL REFERENCES connectivity_probes(id),screening_id TEXT NOT NULL REFERENCES screenings(id),lease TEXT NOT NULL,target BLOB NOT NULL,timeout_ms INTEGER NOT NULL,state TEXT NOT NULL,created INTEGER NOT NULL,expires INTEGER NOT NULL,result BLOB,egress BLOB,report_hash TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS probe_tasks_dispatch ON probe_tasks(probe_id,state,created);
`)
	return err
}
