# Telegram Drive

Telegram Drive is a powerful utility that enables you to organise your telegram files and much more.

[![Discord](https://img.shields.io/discord/1142377485737148479?label=discord&logo=discord&style=flat-square&logoColor=white)](https://discord.gg/8QAeCvTK7G)

**Click on icon to join Discord Server**

## Features

- **UI:** Based on Material You to create nice looking UI themes.
- **Secure:** Your data is secured using robust encryption.
- **Flexible Deployment:** Use Docker Compose or deploy without Docker.

## Advantages Over Alternative Solutions

- **Exceptional Speed:** Teldrive stands out among similar tools, thanks to its implementation in Go, a language known for its efficiency. Its performance surpasses alternatives written in Python and other languages, with the exception of Rust.

- **Enhanced Management Capabilities:** Teldrive not only excels in speed but also offers an intuitive user interface for efficient file interaction which other tool lacks. Its compatibility with Rclone further enhances file management.

> [!IMPORTANT]
> Teldrive functions as a wrapper over your Telegram account, simplifying file access. However, users must adhere to the limitations imposed by the Telegram API. Teldrive is not responsible for any consequences arising from non-compliance with these API limits.You will be banned instantly if you misuse telegram API.

![demo](./public/demo1.png)

<details>
<summary><b>More Images</b></summary>

![demo2](./public/demo2.png)
![demo3](./public/demo3.png)
![demo5](./public/demo6.png)
![demo8](./public/demo8.png)
![demo7](./public/demo7.png)
![demo4](./public/demo4.png)
</details>

<br>

[UI Repo ](https://github.com/divyam234/teldrive-ui)

[UI Library ](https://github.com/divyam234/tw-material)

[File Browser Component ](https://github.com/divyam234/tw-file-browser)

### One Line Installer

[![install count](https://img.shields.io/endpoint?url=https://instl.sh/api/v1/badge/shields.io/stats/divyam234/teldrive&style=for-the-badge)](https://instl.sh/divyam234/teldrive)

#### Linux

```bash
curl -sSL instl.sh/divyam234/teldrive/linux | bash
```

#### Windows

```powershell
iwr instl.sh/divyam234/teldrive/windows | iex
```

#### macOS

```bash
curl -sSL instl.sh/divyam234/teldrive/macos | bash
```

### Deploy using docker-compose

```yml
version: "3.8"

services:
  server:
    image: ghcr.io/divyam234/teldrive
    restart: always
    container_name: teldrive
    volumes:
      - ./session.db:/session.db:rw
      - ./config.toml:/config.toml
    ports:
      - 8080:8080

```
***People Who want to use local Postgres instance***
```yml
version: "3.8"

services:
  server:
    image: ghcr.io/divyam234/teldrive
    restart: always
    container_name: teldrive
    volumes:
      - ./session.db:/session.db:rw
      - ./config.toml:/config.toml
    ports:
      - 8080:8080
    depends_on:
      db:
        condition: service_healthy
  db:
    image: postgres:15
    container_name: teldrive_db
    restart: always
    environment:
      - POSTGRES_USER=teldrive
      - POSTGRES_PASSWORD=secret
    volumes:
      - ./postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "teldrive"]
      interval: 10s
      start_period: 30s
```

**Follow Below Steps**

- Create the `config.toml` file with your values and start your container. See how to fill file below.

```toml
[db]
data-source = "postgres://<db username>:<db password>@<db host>/<db name>"

[jwt]
secret = "abcd"

[tg]
app-id = 
app-hash = "fwfwfwf"
```
***Only these values are mandatory however you can change or
tweak your config see more in advanced configurations below***.
```sh
touch session.db
docker compose up -d
```
- **Go to http://localhost:8080**
> [!WARNING]
> Make sure to run ```touch session.db``` to create empty session file  if you are using docker compose otherwise app will not run.

### Use without docker

**Follow Below Steps**

- Run one line installer.
- Add same config file as above.
- Now, run the Teldrive executable binary directly.
- You can also set up without config file.

```sh
teldrive run --tg-app-id="" --tg-app-hash="" --jwt-secret="" --db-data-source=""
```

**Generate JWT**
```bash
$ openssl rand -hex 32
```
You can generate secret from [here](https://generate-secret.vercel.app/32).

## Important
  - Default Channel can be selected through UI. Make sure to set it from account settings on first login.
  - Multi Bots Mode is recommended to avoid flood errors and enable maximum download speed, especially if you are using downloaders like IDM and aria2c, which use multiple connections for downloads.
  - To enable multi bots, generate new bot tokens from BotFather and add them through UI on first login.
  - Uploads from UI will be slower due to limitations of the browser. Use modified [Rclone](https://github.com/divyam234/rclone) version for teldrive.
  - Teldrive supports image thumbnail resizing on the fly. To enable this, you have to deploy a separate image resize service from [here](https://github.com/divyam234/image-resize).
  - After deploying this service, add its URL in Teldrive UI settings in the **Resize Host** field.
  - Files are deleted at regular interval of one hour through cron job from tg channel after its deleted from teldrive this is done so  that person can recover files if he/she accidently deletes them.

### Advanced Configuration

**cli options**

```sh
teldrive run --help
```
| Flag Name                           | Description                                       | Required | Default Value                                         |
|-------------------------------------|---------------------------------------------------|----------|-------------------------------------------------------|
| --jwt-secret                         | JWT secret key                                    | Yes      | ""                               |
| --db-data-source                     | Database connection string                       | Yes      | ""                               |
| --tg-app-id                          | API ID for your Telegram account, which can be obtained from my.telegram.org.                                   | Yes      | 0                                                     |
| --tg-app-hash                        | API HASH for your Telegram account, which can be obtained from my.telegram.org.                                 | Yes      | ""                              |
| --jwt-allowed-users                  | Allow certain Telegram usernames, including yours, to access the app.                             |No      | ""                        |
| --tg-uploads-encryption-key          | Encryption key for encrypting files.                           | No      | ""                               |
| --config, -c                        | Config file.                                 | No       | $HOME/.teldrive/config.toml                           |
| --server-port, -p                    | Server port                                       | No       | 8080                                                  |
| --log-level                          | Logging level<br> <br> DebugLevel = -1 <br>InfoLevel = 0<br> WarnLevel = 1 <br> ErrorLevel = 2                                     | No       | -1                       |
| --tg-rate-limit                      | Enable rate limiting                              | No       | true                                                  |
| --tg-rate-burst                      | Limiting burst                                    | No       | 5                                                     |
| --tg-rate                            | Limiting rate                                     | No       | 100                                                   |
| --tg-session-file                        | Bot session file.                                 | No       | $HOME/.teldrive/session.db                          |
| --tg-bg-bots-limit                   | Start at most this no of bots in the background to prevent connection recreation on every request.Increase this if you are streaming or downloading large no of files simultaneously.                             | No       | 5                                                                                          
| --tg-uploads-threads                 | Concurrent Uploads threads for uploading file                                  | No       | 8                                                    |
| --tg-uploads-retention               | Uploads retention duration.Duration to keep failed uploaded chunks in db for resuming uploads.                       | No       | 7d                                               |
| --tg-proxy               | Socks5 or HTTP proxy for telegram client.                       | No       | ""                                               |
| --tg-pool-size               | Connection pool size for uploads.Greater pool size will result in more memory and cpu usage set it to 0 but it will lower upload speed significantly or reduce the no of concurrent uploads and transfers in rclone.                  | No       | 8                                              |

**You Can also set config values through env varibles.**
- For example ```tg-session-file``` will become ```TELDRIVE_TG_SESSION_FILE``` same for all possible flags.

- See ```config.sample.toml``` in repo if you want to setup advanced configurations through toml file.

> [!WARNING]
> Keep your Password safe once generated teldrive uses same encryption as of rclone internally 
so you don't need to enable crypt in rclone.**Teldrive generates random salt for each file part and saves in database so its more secure than rclone crypt whereas in rclone same salt value  is used  for all files which can be compromised easily**. Enabling crypt in rclone makes UI redundant so encrypting files in teldrive internally is better way to encrypt files and more secure encryption than rclone.To encrypt files see more about teldrive rclone config.

### For making use of Multi Bots

> [!WARNING]
> Bots will be auto added as admin in channel if you set them from UI if it fails somehow add it manually.For newly logged session you have to wait 20-30 min to add bots to telegram channel.**FRESH_CHANGE_ADMINS_FORBIDDEN** error  will be thrown if you try to add bots before that time frame.

### Rclone Config Example
```conf
[teldrive]
type = teldrive
api_host = http://localhost:8080 # default host
access_token = #session token obtained from cookies
chunk_size = 500M
upload_concurrency = 4
encrypt_files = false # Enable this to encrypt files make sure ENCRYPTION_KEY env variable is not empty in teldrive instance.
random_chunk_name= true # Use random chunk names when uploading files to channel instead of original filename.
```
**See all options in rclone config command**

# Recognitions

<a href="https://trendshift.io/repositories/7568" target="_blank"><img src="https://trendshift.io/api/badge/repositories/7568" alt="divyam234%2Fteldrive | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>

## Best Practices for Using Teldrive

### Dos:

- **Follow Limits:** Adhere to the limits imposed by Telegram servers to avoid account bans and automatic deletion of your channel.Your files will be removed from telegram servers if you try to abuse the service as most people have zero brains they will still do so good luck.
- **Responsible Storage:** Be mindful of the content you store on Telegram. Utilize storage efficiently and only keep data that serves a purpose.
  
### Don'ts:
- **Data Hoarding:** Avoid excessive data hoarding, as it not only violates Telegram's terms.
  
By following these guidelines, you contribute to the responsible and effective use of Telegram, maintaining a fair and equitable environment for all users.


## Contributing

Feel free to contribute to this project if you have any further ideas.

## Donate

If you like this project small contribution would be appreciated [Paypal](https://paypal.me/redux234).
