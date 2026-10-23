# Hosting whatnow

How to run `whatnow host` for other people on a DigitalOcean droplet, with Caddy for HTTPS, user
data on a DigitalOcean Volume, and continuous backups to Cloudflare R2. It assumes you have never
made a Volume or an R2 bucket. Do the sections in order.

What you end up with:

- A droplet running `whatnow host` under systemd, listening only on `127.0.0.1:8080`.
- Caddy in front, doing HTTPS for your domain.
- Every database on a Volume mounted at `/mnt/whatnow`: `accounts.db`, and one `users/<id>.db` per
  person, a new file each time someone signs up. Rebuilding the droplet doesn't touch the Volume.
- Litestream copying every one of those files to R2 continuously, including files that appear
  after it starts, and keeping 30 days of history.

Throughout, `whatnow.example.com` is your domain. Replace it everywhere.

## 1. Create the droplet and the Volume

1. In the DigitalOcean control panel, choose **Create > Droplets**. Pick Ubuntu 24.04 LTS, the
   smallest Basic plan, and a region. Add your SSH key. Create it.
2. Choose **Create > Volumes**. Make it 10 GiB (it can be grown later), in the **same region**, and
   attach it to the droplet. When asked, choose **Manually Format & Mount**.
3. Under **Networking > Firewalls**, create a cloud firewall that allows inbound **22, 80, and 443**
   only, and apply it to the droplet.
4. Point your domain at the droplet: add an `A` record for `whatnow.example.com` with the droplet's
   public IP.

The rest of this guide runs on the droplet: `ssh root@whatnow.example.com`.

Keep every other service on this droplet bound to loopback (`127.0.0.1`), never `0.0.0.0`. whatnow
refuses to fetch private addresses on a user's behalf, but it can still reach the droplet's own
public IP, so anything listening there could be reached through it. Run nothing else here if you
can.

## 2. Mount the Volume at /mnt/whatnow

Find the Volume's device:

```sh
ls /dev/disk/by-id/ | grep scsi-0DO_Volume
```

You'll see a name like `scsi-0DO_Volume_volume-nyc1-01`. Format it once (this erases it, so only
ever do this on a new, empty Volume), mount it, and make the mount survive reboots:

```sh
DEV=/dev/disk/by-id/scsi-0DO_Volume_volume-nyc1-01   # use the name you saw
mkfs.ext4 "$DEV"
mkdir -p /mnt/whatnow
echo "$DEV /mnt/whatnow ext4 defaults,nofail,discard 0 2" >> /etc/fstab
mount -a
df -h /mnt/whatnow
```

`df` should show the Volume's size. `nofail` lets the droplet boot even if the Volume is missing.

## 3. Create the whatnow user

```sh
useradd --system --home /mnt/whatnow --shell /usr/sbin/nologin whatnow
chown whatnow:whatnow /mnt/whatnow
chmod 700 /mnt/whatnow
```

## 4. Install Caddy

```sh
apt update
apt install -y debian-keyring debian-archive-keyring apt-transport-https curl gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list
apt update
apt install -y caddy
```

If those lines have changed, the current ones are at <https://caddyserver.com/docs/install>.

Replace `/etc/caddy/Caddyfile` with:

```
whatnow.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

```sh
systemctl reload caddy
```

Caddy fetches and renews the HTTPS certificate on its own, as long as the `A` record from section
1 has propagated and ports 80 and 443 are open.

## 5. Create the Google OAuth client

whatnow signs people in with Google.

1. Go to <https://console.cloud.google.com/> and create a project (or pick one).
2. **APIs & Services > OAuth consent screen**: choose **External**, fill in the app name, support
   email, and your domain. Scopes: leave the defaults (`openid`, `email`, `profile`). Publish the
   app to **In production**, or only listed test users can sign in.
3. **APIs & Services > Credentials > Create credentials > OAuth client ID**. Application type:
   **Web application**. Under **Authorized redirect URIs**, add exactly:

   ```
   https://whatnow.example.com/auth/google/callback
   ```

4. Copy the **Client ID** and **Client secret**. You need them in section 6.

## 6. Generate the seal key and write the environment file

whatnow encrypts each user's model API keys with a seal key before storing them in their database.
Generate it on your own machine or the droplet:

```sh
openssl rand -base64 32
```

**Copy it somewhere safe that is not this server**, such as your password manager. It is kept off
the Volume on purpose, and you must keep a copy elsewhere:

- The Volume is what gets backed up. Because the key isn't on it, a stolen or leaked backup can't
  decrypt anyone's API keys.
- The flip side: if you lose the key, the encrypted keys in the databases (and in every backup) are
  unreadable, and every user has to enter their API key again. A restore onto a new droplet needs
  the same key.

Write the environment file on the droplet, readable by root only:

```sh
install -m 600 -o root -g root /dev/null /etc/whatnow.env
cat > /etc/whatnow.env <<'ENV'
WHATNOW_GOOGLE_CLIENT_ID=paste-the-client-id
WHATNOW_GOOGLE_CLIENT_SECRET=paste-the-client-secret
WHATNOW_SEAL_KEY=paste-the-output-of-openssl
ENV
ls -l /etc/whatnow.env   # -rw------- root root
```

systemd reads this file as root and hands the values to the service, so the `whatnow` user never
needs to read it.

## 7. Install the binary and the systemd unit

On your own machine, from the repo:

```sh
just build-linux
scp dist/whatnow-linux-amd64 root@whatnow.example.com:/usr/local/bin/whatnow
```

On the droplet:

```sh
chmod 755 /usr/local/bin/whatnow
cat > /etc/systemd/system/whatnow.service <<'UNIT'
[Unit]
Description=whatnow (hosted)
After=network-online.target mnt-whatnow.mount
Wants=network-online.target
RequiresMountsFor=/mnt/whatnow

[Service]
User=whatnow
Group=whatnow
EnvironmentFile=/etc/whatnow.env
ExecStart=/usr/local/bin/whatnow host -addr 127.0.0.1:8080 -data /mnt/whatnow -public-url https://whatnow.example.com -max-users 100
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/mnt/whatnow
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now whatnow
systemctl status whatnow
journalctl -u whatnow -f
```

`-max-users` is the sign-up cap (default 100); change it here and restart to raise it.

Visit `https://whatnow.example.com` and sign in with Google to check the whole chain. Because the
Volume is mounted with `nofail`, `RequiresMountsFor` keeps the service from starting against an
empty directory if the mount ever fails.

## 8. Create the R2 bucket and an API token

1. Sign in at <https://dash.cloudflare.com/> and open **R2 Object Storage** in the sidebar. The
   first time, you must add a payment method; R2's free tier is generous, and there is no charge for
   downloads out of R2.
2. **Create bucket**. Name it `whatnow-backups`. Leave the location on Automatic (or pick the
   region nearest your droplet).
3. On the R2 overview page, copy your **Account ID** (right-hand side).
4. **Manage R2 API Tokens > Create API token**. Permission: **Object Read & Write**. Under
   **Specify bucket(s)**, choose `whatnow-backups` only. Create it.
5. Copy the **Access Key ID** and **Secret Access Key** now; the secret is shown once.

## 9. Install Litestream and configure backups

[Litestream](https://litestream.io/) watches a SQLite database and streams every change to object
storage. Pointed at a directory with `watch: true`, it picks up new database files within seconds,
so each new sign-up's file is backed up too.

Install it (0.5.x; check <https://github.com/benbjohnson/litestream/releases> for the latest):

```sh
cd /tmp
curl -LO https://github.com/benbjohnson/litestream/releases/download/v0.5.17/litestream-0.5.17-linux-amd64.deb
dpkg -i litestream-0.5.17-linux-amd64.deb
```

Write the config, readable by root only because it holds the R2 keys:

```sh
install -m 600 -o root -g root /dev/null /etc/litestream.yml
cat > /etc/litestream.yml <<'YAML'
# Keep 30 days: a snapshot a day, and each snapshot (with the changes since it) lives 720 hours.
snapshot:
  interval: 24h
  retention: 720h

dbs:
  - dir: /mnt/whatnow
    pattern: "*.db"
    recursive: true
    watch: true
    replica:
      type: s3
      bucket: whatnow-backups
      path: whatnow
      endpoint: https://YOUR-ACCOUNT-ID.r2.cloudflarestorage.com
      region: auto
      access-key-id: PASTE-ACCESS-KEY-ID
      secret-access-key: PASTE-SECRET-ACCESS-KEY
YAML
systemctl enable --now litestream
systemctl status litestream
journalctl -u litestream -n 50
```

The journal should show `accounts.db` and each existing `users/*.db` being replicated. Sign in with
a second Google account, then check the journal again: a new `users/<id>.db` should appear within
seconds. In the Cloudflare dashboard, the bucket now holds `whatnow/accounts.db/...` and
`whatnow/users/<id>.db/...`.

Things to know:

- The bucket layout mirrors the directory, so `/mnt/whatnow/users/abc.db` lives under
  `whatnow/users/abc.db/`.
- When a user deletes their account, Litestream stops replicating their file, but their existing
  backup stays in R2. Remove it yourself by deleting that prefix in the bucket if you need it gone.
- Litestream only replicates files with a valid SQLite header, so stray files matching `*.db` are
  ignored.

## 10. The restore drill

Do this once before you open to users, and again whenever you change the setup. It proves the
backups work and that you can read them without the original server.

On a scratch machine (a throwaway droplet, or any Linux box with `sqlite3`), install Litestream the
same way, then:

```sh
export LITESTREAM_ACCESS_KEY_ID=PASTE-ACCESS-KEY-ID
export LITESTREAM_SECRET_ACCESS_KEY=PASTE-SECRET-ACCESS-KEY
R2='endpoint=https://YOUR-ACCOUNT-ID.r2.cloudflarestorage.com&region=auto'
mkdir -p /tmp/restore/users

litestream restore -o /tmp/restore/accounts.db "s3://whatnow-backups/whatnow/accounts.db?$R2"
sqlite3 /tmp/restore/accounts.db 'select id, email, created_at from users;'

ID=pick-an-id-from-the-list-above
litestream restore -o /tmp/restore/users/$ID.db "s3://whatnow-backups/whatnow/users/$ID.db?$R2"
sqlite3 /tmp/restore/users/$ID.db '.tables' 'select count(*) from tasks;'
```

If both open and the rows look right, the drill passes. To restore to an earlier moment, add
`-timestamp 2026-01-01T12:00:00Z` before the URL.

Litestream can't restore every database in one command when it replicates a directory. To rebuild
a whole server, list the users from the restored `accounts.db` and restore `users/<id>.db` for each.
Put the files at `/mnt/whatnow/accounts.db` and `/mnt/whatnow/users/<id>.db`, owned by `whatnow`,
set `WHATNOW_SEAL_KEY` to your saved copy, and start the service. Stop `litestream` on the new
server until the restore is done so it doesn't start replicating partial files.

## 11. Deploy an update

On your own machine:

```sh
just build-linux
scp dist/whatnow-linux-amd64 root@whatnow.example.com:/usr/local/bin/whatnow.new
ssh root@whatnow.example.com 'mv /usr/local/bin/whatnow.new /usr/local/bin/whatnow && systemctl restart whatnow'
```

Check `journalctl -u whatnow -n 20` for a clean start. Users stay signed in across a restart, because sessions
live in `accounts.db`.
