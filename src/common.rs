use clap::App;
use hbb_common::{
    anyhow::{Context, Result}, log, ResultType
};
use ini::Ini;
use sodiumoxide::crypto::sign;
use std::{
    io::prelude::*,
    net::SocketAddr,
    time::{Instant, SystemTime},
};

#[allow(dead_code)]
pub(crate) fn get_expired_time() -> Instant {
    let now = Instant::now();
    now.checked_sub(std::time::Duration::from_secs(3600))
        .unwrap_or(now)
}

#[allow(dead_code)]
pub(crate) fn test_if_valid_server(host: &str, name: &str) -> ResultType<SocketAddr> {
    use std::net::ToSocketAddrs;
    let res = if host.contains(':') {
        host.to_socket_addrs()?.next().context("")
    } else {
        format!("{}:{}", host, 0)
            .to_socket_addrs()?
            .next()
            .context("")
    };
    if res.is_err() {
        log::error!("Invalid {} {}: {:?}", name, host, res);
    }
    res
}

#[allow(dead_code)]
pub(crate) fn get_servers(s: &str, tag: &str) -> Vec<String> {
    let servers: Vec<String> = s
        .split(',')
        .filter(|x| !x.is_empty() && test_if_valid_server(x, tag).is_ok())
        .map(|x| x.to_owned())
        .collect();
    log::info!("{}={:?}", tag, servers);
    servers
}

#[allow(dead_code)]
#[inline]
fn arg_name(name: &str) -> String {
    name.to_uppercase().replace('_', "-")
}

#[allow(dead_code)]
pub fn init_args(args: &str, name: &str, about: &str) {
    let matches = App::new(name)
        .version(crate::version::VERSION)
        .author("Purslane Ltd. <info@rustdesk.com>")
        .about(about)
        .args_from_usage(args)
        .get_matches();
    if let Ok(v) = Ini::load_from_file(".env") {
        if let Some(section) = v.section(None::<String>) {
            section
                .iter()
                .for_each(|(k, v)| std::env::set_var(arg_name(k), v));
        }
    }
    if let Some(config) = matches.value_of("config") {
        if let Ok(v) = Ini::load_from_file(config) {
            if let Some(section) = v.section(None::<String>) {
                section
                    .iter()
                    .for_each(|(k, v)| std::env::set_var(arg_name(k), v));
            }
        }
    }
    for (k, v) in matches.args {
        if let Some(v) = v.vals.first() {
            std::env::set_var(arg_name(k), v.to_string_lossy().to_string());
        }
    }
}

#[allow(dead_code)]
#[inline]
pub fn get_arg(name: &str) -> String {
    get_arg_or(name, "".to_owned())
}

#[allow(dead_code)]
#[inline]
pub fn get_arg_or(name: &str, default: String) -> String {
    std::env::var(arg_name(name)).unwrap_or(default)
}

#[allow(dead_code)]
#[inline]
pub fn now() -> u64 {
    SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .map(|x| x.as_secs())
        .unwrap_or_default()
}

pub fn gen_sk(wait: u64) -> (String, Option<sign::SecretKey>) {
    if wait > 0 && !std::path::Path::new("id_ed25519").exists() {
        std::thread::sleep(std::time::Duration::from_millis(wait));
    }
    match load_or_create_key(std::path::Path::new(".")) {
        Ok((pk, sk)) => (pk, Some(sk)),
        Err(err) => {
            eprintln!("Cannot initialize server identity: {err}");
            std::process::exit(1);
        }
    }
}

fn load_or_create_key(dir: &std::path::Path) -> Result<(String, sign::SecretKey)> {
    let path = dir.join("id_ed25519");
    if let Ok(meta) = std::fs::symlink_metadata(&path) {
        if !meta.is_file() || meta.file_type().is_symlink() { hbb_common::bail!("private key must be a regular file"); }
    }
    let sk = match std::fs::read_to_string(&path) {
        Ok(contents) => {
            let bytes = base64::decode(contents.trim())?;
            sign::SecretKey::from_slice(&bytes).context("malformed private key")?
        }
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            let (_, sk) = sign::gen_keypair();
            let mut file = tempfile::NamedTempFile::new_in(dir)?;
            file.write_all(base64::encode(&sk).as_bytes())?;
            file.as_file().sync_all()?;
            match file.persist_noclobber(&path) {
                Ok(_) => sk,
                Err(e) if e.error.kind() == std::io::ErrorKind::AlreadyExists => return load_or_create_key(dir),
                Err(e) => return Err(e.error.into()),
            }
        }
        Err(e) => return Err(e.into()),
    };
    #[cfg(unix)] {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))?;
    }
    let pk = base64::encode(&sk.0[sign::SECRETKEYBYTES / 2..]);
    // Recover only a missing/mismatched public companion; never rotate private identity.
    let public_path = dir.join("id_ed25519.pub");
    if std::fs::read_to_string(&public_path).ok().as_deref() != Some(&pk) {
        let mut public = tempfile::NamedTempFile::new_in(dir)?;
        public.write_all(pk.as_bytes())?;
        public.as_file().sync_all()?;
        #[cfg(unix)] {
            use std::os::unix::fs::PermissionsExt;
            public.as_file().set_permissions(std::fs::Permissions::from_mode(0o644))?;
        }
        public.persist(&public_path).map_err(|e| e.error)?;
    }
    Ok((pk, sk))
}

#[cfg(test)]
mod key_tests {
    #[test]
    fn identity_is_persistent_and_invalid_keys_fail_closed() {
        let dir = tempfile::tempdir().unwrap();
        let (pk, _) = super::load_or_create_key(dir.path()).unwrap();
        assert_eq!(pk, super::load_or_create_key(dir.path()).unwrap().0);
        #[cfg(unix)] {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(std::fs::metadata(dir.path().join("id_ed25519")).unwrap().permissions().mode() & 0o777, 0o600);
        }
        std::fs::write(dir.path().join("id_ed25519"), "invalid").unwrap();
        assert!(super::load_or_create_key(dir.path()).is_err());
        assert_eq!(std::fs::read_to_string(dir.path().join("id_ed25519")).unwrap(), "invalid");
    }
}

/// Filesystem-authenticated control plane; never multiplex commands onto public TCP.
#[cfg(unix)]
pub fn control_listener() -> Result<Option<hbb_common::tokio::net::UnixListener>> {
    use std::os::unix::fs::{DirBuilderExt, FileTypeExt, PermissionsExt};
    let path = std::env::var("CONTROL_SOCKET").unwrap_or_default();
    if path.is_empty() { return Ok(None); }
    let socket_path = std::path::Path::new(&path);
    if !socket_path.is_absolute() { hbb_common::bail!("control socket path must be absolute"); }
    let parent = socket_path.parent().context("control socket needs a directory")?;
    match std::fs::DirBuilder::new().mode(0o700).create(parent) {
        Ok(()) => {},
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {},
        Err(e) => return Err(e.into()),
    }
    let meta = std::fs::symlink_metadata(parent)?;
    if !meta.is_dir() || meta.file_type().is_symlink() || meta.permissions().mode() & 0o077 != 0 {
        hbb_common::bail!("control socket directory must be private (0700)");
    }
    if let Ok(meta) = std::fs::symlink_metadata(&path) {
        if !meta.file_type().is_socket() { hbb_common::bail!("control path is not a socket"); }
        match std::os::unix::net::UnixStream::connect(&path) {
            Ok(_) => hbb_common::bail!("control socket is already in use"),
            Err(e) if e.kind() == std::io::ErrorKind::ConnectionRefused => std::fs::remove_file(&path)?,
            Err(e) => return Err(e.into()),
        }
    }
    let listener = hbb_common::tokio::net::UnixListener::bind(&path)?;
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))?;
    Ok(Some(listener))
}

#[cfg(unix)]
pub async fn serve_control<F, Fut>(listener: hbb_common::tokio::net::UnixListener, handler: F) -> Result<()>
where F: Fn(String) -> Fut, Fut: std::future::Future<Output = String> {
    use hbb_common::tokio::{io::{AsyncReadExt, AsyncWriteExt}, time::{timeout, Duration}};
    loop {
        let (mut stream, _) = listener.accept().await?;
        let mut command = Vec::new();
        let result = timeout(Duration::from_secs(2), (&mut stream).take(1025).read_to_end(&mut command)).await;
        if !matches!(result, Ok(Ok(_))) || command.is_empty() || command.len() > 1024 { continue; }
        let command = match String::from_utf8(command) { Ok(v) => v, Err(_) => continue };
        if command.contains(['\r', '\n', '\0']) { continue; }
        if let Ok(response) = timeout(Duration::from_secs(2), handler(command)).await {
            if response.len() <= 65536 {
                let _ = timeout(Duration::from_secs(2), stream.write_all(response.as_bytes())).await;
            }
        }
    }
}

#[cfg(unix)]
pub async fn listen_signal() -> Result<()> {
    use hbb_common::tokio;
    use hbb_common::tokio::signal::unix::{signal, SignalKind};

    tokio::spawn(async {
        let mut s = signal(SignalKind::terminate())?;
        let terminate = s.recv();
        let mut s = signal(SignalKind::interrupt())?;
        let interrupt = s.recv();
        let mut s = signal(SignalKind::quit())?;
        let quit = s.recv();

        tokio::select! {
            _ = terminate => {
                log::info!("signal terminate");
            }
            _ = interrupt => {
                log::info!("signal interrupt");
            }
            _ = quit => {
                log::info!("signal quit");
            }
        }
        Ok(())
    })
    .await?
}

#[cfg(not(unix))]
pub async fn listen_signal() -> Result<()> {
    let () = std::future::pending().await;
    unreachable!();
}
