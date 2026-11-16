use async_trait::async_trait;
use hbb_common::{log, ResultType};
use sqlx::{
    sqlite::{SqliteConnectOptions, SqliteJournalMode}, ConnectOptions, Connection, Error as SqlxError, SqliteConnection,
};
use std::{ops::DerefMut, str::FromStr};
//use sqlx::postgres::PgPoolOptions;
//use sqlx::mysql::MySqlPoolOptions;

type Pool = deadpool::managed::Pool<DbPool>;

pub struct DbPool {
    options: SqliteConnectOptions,
}

#[async_trait]
impl deadpool::managed::Manager for DbPool {
    type Type = SqliteConnection;
    type Error = SqlxError;
    async fn create(&self) -> Result<SqliteConnection, SqlxError> {
        SqliteConnection::connect_with(&self.options).await
    }
    async fn recycle(
        &self,
        obj: &mut SqliteConnection,
    ) -> deadpool::managed::RecycleResult<SqlxError> {
        Ok(obj.ping().await?)
    }
}

#[derive(Clone)]
pub struct Database {
    pool: Pool,
}

#[derive(Default)]
pub struct Peer {
    pub guid: Vec<u8>,
    pub id: String,
    pub uuid: Vec<u8>,
    pub pk: Vec<u8>,
    pub user: Option<Vec<u8>>,
    pub info: String,
    pub status: Option<i64>,
}

impl Database {
    pub async fn new(url: &str) -> ResultType<Database> {
        let mut options = SqliteConnectOptions::from_str(url)?
            .create_if_missing(true)
            .foreign_keys(true)
            .journal_mode(SqliteJournalMode::Wal)
            .busy_timeout(std::time::Duration::from_secs(5));
        options.log_statements(log::LevelFilter::Debug);
        let n: usize = std::env::var("MAX_DATABASE_CONNECTIONS")
            .unwrap_or_else(|_| "1".to_owned())
            .parse()?;
        if !(1..=64).contains(&n) {
            hbb_common::bail!("MAX_DATABASE_CONNECTIONS must be between 1 and 64");
        }
        let pool = Pool::new(DbPool { options }, n);
        let _ = pool.get().await?; // test
        let db = Database { pool };
        db.create_tables().await?;
        Ok(db)
    }

    async fn create_tables(&self) -> ResultType<()> {
        sqlx::query!(
            "
            create table if not exists peer (
                guid blob primary key not null,
                id varchar(100) not null,
                uuid blob not null,
                pk blob not null,
                created_at datetime not null default(current_timestamp),
                user blob,
                status tinyint,
                note varchar(300),
                info text not null
            ) without rowid;
            create unique index if not exists index_peer_id on peer (id);
            create index if not exists index_peer_user on peer (user);
            create index if not exists index_peer_created_at on peer (created_at);
            create index if not exists index_peer_status on peer (status);
        "
        )
        .execute(self.pool.get().await?.deref_mut())
        .await?;
        Ok(())
    }

    pub async fn get_peer(&self, id: &str) -> ResultType<Option<Peer>> {
        Ok(sqlx::query_as!(
            Peer,
            "select guid, id, uuid, pk, user, status, info from peer where id = ?",
            id
        )
        .fetch_optional(self.pool.get().await?.deref_mut())
        .await?)
    }

    pub async fn insert_peer(
        &self,
        id: &str,
        uuid: &[u8],
        pk: &[u8],
        info: &str,
    ) -> ResultType<Vec<u8>> {
        let guid = uuid::Uuid::new_v4().as_bytes().to_vec();
        sqlx::query!(
            "insert into peer(guid, id, uuid, pk, info) values(?, ?, ?, ?, ?)",
            guid,
            id,
            uuid,
            pk,
            info
        )
        .execute(self.pool.get().await?.deref_mut())
        .await?;
        Ok(guid)
    }

    pub async fn update_pk(
        &self,
        guid: &Vec<u8>,
        id: &str,
        pk: &[u8],
        info: &str,
    ) -> ResultType<()> {
        sqlx::query!(
            "update peer set id=?, pk=?, info=? where guid=?",
            id,
            pk,
            info,
            guid
        )
        .execute(self.pool.get().await?.deref_mut())
        .await?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use hbb_common::tokio;
    #[tokio::test]
    async fn database_is_repeatable_and_enforces_constraints() {
        use std::ops::DerefMut;
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("peers.sqlite3");
        let db = super::Database::new(path.to_str().unwrap()).await.unwrap();
        let mut jobs = vec![];
        for i in 0..32 {
            let db = db.clone();
            jobs.push(tokio::spawn(async move {
                let id = i.to_string();
                db.insert_peer(&id, &[1], &[2], "{}").await.unwrap();
                assert_eq!(db.get_peer(&id).await.unwrap().unwrap().uuid, vec![1]);
            }));
        }
        for result in hbb_common::futures::future::join_all(jobs).await {
            result.unwrap();
        }
        assert!(db.insert_peer("0", &[3], &[4], "{}").await.is_err());
        let mut conn = db.pool.get().await.unwrap();
        let journal: String = sqlx::query_scalar("PRAGMA journal_mode").fetch_one(conn.deref_mut()).await.unwrap();
        let foreign_keys: i64 = sqlx::query_scalar("PRAGMA foreign_keys").fetch_one(conn.deref_mut()).await.unwrap();
        assert_eq!(journal, "wal");
        assert_eq!(foreign_keys, 1);
        drop(conn);
        assert!(super::Database::new("sqlite://invalid?mode=bogus").await.is_err());
        assert!(super::Database::new(dir.path().join("missing/db").to_str().unwrap()).await.is_err());
    }
}
