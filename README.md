# e18e-bot

Now you can know how much the e has 18e'd.

## How to run

1. Make sure you have Go 1.21 or newer installed.

2. Create a config file by copying the example:

    ```
    cp config/config.go.example config/config.go
    ```

3. [Create a Discord app](https://discord.com/developers/applications) and invite it to a server. Make sure it has the `applications.commands` and `bot` scopes. Add its application ID and bot token to the config.

4. Run the bot:

    ```
    go run .
    ```
