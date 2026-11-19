# Components V2 survey

Install the bot in a test server and give it permission to view and send messages
in the target channel. Set `DISCORD_BOT_TOKEN` to the bot token, then run this
command from the repository root:

```sh
go run ./examples/components_v2 -channel CHANNEL_ID
```

Click **Choose a topic** on the posted message, select a radio option, and
submit the modal. The bot defers an ephemeral response, then edits that response
with a Components V2 text display showing the selection. The original survey
stays available to the channel.
