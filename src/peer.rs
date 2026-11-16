use crate::common::*;
use crate::database;
use hbb_common::{
    bytes::Bytes,
    log,
    rendezvous_proto::*,
    tokio::sync::{Mutex, RwLock},
    ResultType,
};
use serde_derive::{Deserialize, Serialize};
use std::{collections::HashMap, collections::HashSet, net::SocketAddr, sync::Arc, time::Instant};

type IpBlockMap = HashMap<String, ((u32, Instant), (HashSet<String>, Instant))>;
type IpChangesMap = HashMap<String, (Instant, HashMap<String, i32>)>;
lazy_static::lazy_static! {
    pub(crate) static ref IP_BLOCKER: Mutex<IpBlockMap> = Default::default();
    pub(crate) static ref IP_CHANGES: Mutex<IpChangesMap> = Default::default();
}
pub const IP_CHANGE_DUR: u64 = 180;
pub const IP_CHANGE_DUR_X2: u64 = IP_CHANGE_DUR * 2;
pub const DAY_SECONDS: u64 = 3600 * 24;
pub const IP_BLOCK_DUR: u64 = 60;
pub const MAX_TRACKED_ENTRIES: usize = 10_000;
const PEER_IDLE_SECONDS: u64 = 3600;

#[derive(Debug, Default, Serialize, Deserialize, Clone)]
pub(crate) struct PeerInfo {
    #[serde(default)]
    pub(crate) ip: String,
}

pub(crate) struct Peer {
    pub(crate) socket_addr: SocketAddr,
    pub(crate) last_reg_time: Instant,
    pub(crate) guid: Vec<u8>,
    pub(crate) uuid: Bytes,
    pub(crate) pk: Bytes,
    // pub(crate) user: Option<Vec<u8>>,
    pub(crate) info: PeerInfo,
    // pub(crate) disabled: bool,
    pub(crate) reg_pk: (u32, Instant), // how often register_pk
}

impl Default for Peer {
    fn default() -> Self {
        Self {
            socket_addr: "0.0.0.0:0".parse().unwrap(),
            last_reg_time: get_expired_time(),
            guid: Vec::new(),
            uuid: Bytes::new(),
            pk: Bytes::new(),
            info: Default::default(),
            // user: None,
            // disabled: false,
            reg_pk: (0, get_expired_time()),
        }
    }
}

pub(crate) type LockPeer = Arc<RwLock<Peer>>;

fn reserve_peer_slot(map: &mut HashMap<String, LockPeer>, limit: usize) -> bool {
    if map.len() >= limit {
        map.retain(|_, peer| {
            // Never detach a live/in-flight peer and create a second identity lock.
            Arc::strong_count(peer) > 1 || peer.try_read()
                .map(|p| p.last_reg_time.elapsed().as_secs() < PEER_IDLE_SECONDS)
                .unwrap_or(true)
        });
    }
    map.len() < limit
}

#[derive(Clone)]
pub(crate) struct PeerMap {
    map: Arc<RwLock<HashMap<String, LockPeer>>>,
    pub(crate) db: database::Database,
}

impl PeerMap {
    pub(crate) async fn new() -> ResultType<Self> {
        let db = std::env::var("DB_URL").unwrap_or({
            let mut db = "db_v2.sqlite3".to_owned();
            #[cfg(all(windows, not(debug_assertions)))]
            {
                if let Some(path) = hbb_common::config::Config::icon_path().parent() {
                    db = format!("{}\\{}", path.to_str().unwrap_or("."), db);
                }
            }
            #[cfg(not(windows))]
            {
                db = format!("./{db}");
            }
            db
        });
        let pm = Self {
            map: Default::default(),
            db: database::Database::new(&db).await?,
        };
        Ok(pm)
    }

    #[inline]
    pub(crate) async fn update_pk(
        &mut self,
        id: String,
        peer: LockPeer,
        addr: SocketAddr,
        uuid: Bytes,
        pk: Bytes,
        ip: String,
    ) -> register_pk_response::Result {
        // Serialize identity checks, persistence and publication for the same peer.
        let mut w = peer.write().await;
        if !w.uuid.is_empty() && (w.uuid != uuid || (w.info.ip != ip && w.pk != pk)) {
            return register_pk_response::Result::UUID_MISMATCH;
        }
        let info = PeerInfo { ip };
        let info_str = serde_json::to_string(&info).unwrap_or_default();
        if w.guid.is_empty() {
            match self.db.insert_peer(&id, &uuid, &pk, &info_str).await {
                Ok(guid) => w.guid = guid,
                Err(err) => {
                    log::error!("db.insert_peer failed: {}", err);
                    return register_pk_response::Result::SERVER_ERROR;
                }
            }
        } else if let Err(err) = self.db.update_pk(&w.guid, &id, &pk, &info_str).await {
            log::error!("db.update_pk failed: {}", err);
            return register_pk_response::Result::SERVER_ERROR;
        }
        w.socket_addr = addr;
        w.uuid = uuid;
        w.pk = pk;
        w.last_reg_time = Instant::now();
        w.info = info;
        register_pk_response::Result::OK
    }

    async fn load(&self, id: &str, create: bool) -> ResultType<Option<LockPeer>> {
        if id.is_empty() || id.len() > 100 { hbb_common::bail!("invalid peer id"); }
        if let Some(p) = self.map.read().await.get(id).cloned() { return Ok(Some(p)); }
        // A failed lookup must never become a new identity.
        let stored = self.db.get_peer(id).await?;
        let mut map = self.map.write().await;
        if let Some(p) = map.get(id) { return Ok(Some(p.clone())); }
        let peer = match stored {
            Some(v) => Peer {
                guid: v.guid, uuid: v.uuid.into(), pk: v.pk.into(),
                info: serde_json::from_str::<PeerInfo>(&v.info).unwrap_or_default(),
                ..Default::default()
            },
            None if create => Peer::default(),
            None => return Ok(None),
        };
        if !reserve_peer_slot(&mut map, MAX_TRACKED_ENTRIES) {
            hbb_common::bail!("peer cache capacity reached");
        }
        let peer = Arc::new(RwLock::new(peer));
        map.insert(id.to_owned(), peer.clone());
        Ok(Some(peer))
    }

    pub(crate) async fn get(&self, id: &str) -> Option<LockPeer> {
        self.load(id, false).await.ok().flatten()
    }

    pub(crate) async fn get_or(&self, id: &str) -> ResultType<LockPeer> {
        self.load(id, true).await?.ok_or_else(|| hbb_common::anyhow::anyhow!("peer unavailable"))
    }

    #[inline]
    pub(crate) async fn get_in_memory(&self, id: &str) -> Option<LockPeer> {
        self.map.read().await.get(id).cloned()
    }

    #[inline]
    pub(crate) async fn is_in_memory(&self, id: &str) -> bool {
        self.map.read().await.contains_key(id)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use hbb_common::tokio;

    #[test]
    fn cache_does_not_evict_active_or_inflight_peers() {
        let mut map = HashMap::new();
        let inflight = LockPeer::default();
        map.insert("inflight".into(), inflight.clone());
        map.insert("active".into(), Arc::new(RwLock::new(Peer {
            last_reg_time: Instant::now(), ..Default::default()
        })));
        map.insert("idle".into(), LockPeer::default());
        assert!(reserve_peer_slot(&mut map, 3));
        assert!(!map.contains_key("idle"));
        assert!(map.contains_key("inflight"));
        assert!(!reserve_peer_slot(&mut map, 2));
        drop(inflight);
        assert!(reserve_peer_slot(&mut map, 2));
        assert!(map.contains_key("active"));
    }

    #[tokio::test]
    async fn concurrent_registration_shares_identity_and_failure_is_not_published() {
        let dir = tempfile::tempdir().unwrap();
        let pm = PeerMap {
            map: Default::default(),
            db: database::Database::new(dir.path().join("peers.db").to_str().unwrap()).await.unwrap(),
        };
        let mut jobs = Vec::new();
        for _ in 0..32 {
            let mut pm = pm.clone();
            jobs.push(tokio::spawn(async move {
                let peer = pm.get_or("123456").await.unwrap();
                let result = pm.update_pk("123456".into(), peer.clone(), "127.0.0.1:1234".parse().unwrap(),
                    Bytes::from_static(b"device"), Bytes::from(vec![1;32]), "127.0.0.1".into()).await;
                assert_eq!(result, register_pk_response::Result::OK);
                peer
            }));
        }
        let peer = pm.get_or("123456").await.unwrap();
        for result in hbb_common::futures::future::join_all(jobs).await {
            assert!(Arc::ptr_eq(&peer, &result.unwrap()));
        }
        assert!(!peer.read().await.guid.is_empty());
        let pending = pm.get_or("654321").await.unwrap();
        pm.db.insert_peer("654321", b"existing", &[2;32], "{}").await.unwrap();
        let result = pm.clone().update_pk("654321".into(), pending.clone(), "127.0.0.1:1234".parse().unwrap(),
            Bytes::from_static(b"impostor"), Bytes::from(vec![3;32]), "127.0.0.1".into()).await;
        assert_eq!(result, register_pk_response::Result::SERVER_ERROR);
        assert!(pending.read().await.uuid.is_empty());
        assert!(pending.read().await.guid.is_empty());
        assert_eq!(pm.db.get_peer("654321").await.unwrap().unwrap().uuid, b"existing");
    }
}
