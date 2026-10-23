"""Seed only an isolated soak database with real-density monitoring history."""
import sqlite3
import time
from pathlib import Path


def seed_history(root, hours):
    path=Path(root)/'runtime/lanbridge-go.sqlite3'
    db=sqlite3.connect(path,timeout=30)
    try:
        ids=[r[0] for r in db.execute('SELECT id FROM monitors ORDER BY id')]
        now=int(time.time())//60*60
        before={t:db.execute(f'SELECT COUNT(*) FROM {t}').fetchone()[0] for t in ('samples','aggregates')}
        if hours:
            db.execute('BEGIN IMMEDIATE')
            db.executemany('INSERT INTO samples(monitor_id,at,ok,latency) VALUES(?,?,1,10)',
                           ((i,at) for at in range(now-hours*3600,now,20) for i in ids))
            db.executemany('INSERT INTO aggregates(monitor_id,bucket,resolution,total,up,latency_sum) VALUES(?,?,60,3,3,30) ON CONFLICT(monitor_id,bucket,resolution) DO NOTHING',
                           ((i,at) for at in range(now-hours*3600,now,60) for i in ids))
            db.commit()
        db.execute('PRAGMA wal_checkpoint(TRUNCATE)')
        queries={
            'delete_old_aggregates':('DELETE FROM aggregates WHERE bucket<?',(now-365*86400,)),
            'delete_old_samples':('DELETE FROM samples WHERE at<?',(now-48*3600,)),
            'compact_candidates':('SELECT monitor_id,bucket,total,up,latency_sum FROM aggregates WHERE resolution=? AND bucket<?',(60,now-48*3600))}
        plans={name:list(db.execute('EXPLAIN QUERY PLAN '+q,args)) for name,(q,args) in queries.items()}
        counts={t:db.execute(f'SELECT COUNT(*) FROM {t}').fetchone()[0] for t in ('samples','aggregates')}
        return {'hours':hours,'monitors':len(ids),'database_bytes':path.stat().st_size,'counts_before_seed':before,'counts_after_seed':counts,'query_plans':plans}
    finally:db.close()
