# Trending on Fedi

The Trending on Fedi bot is a simple [Mastodon](https://joinmastodon.org/) bot that pulls posts from the server's public timeline (often called the [Fediverse](https://en.wikipedia.org/wiki/Fediverse)) and aggregates them, posting the top words of a given time interval.

The bot in its canonical form is hosted at [@trendingonfedi@botsin.space](https://botsin.space/@trendingonfedi).

## Usage

Simply download (or compile) a release, edit the ignore.txt file as necessary, and add in a `config.json` file like so:

```json
{
  "credentials": {
    "server": "https://yourmastodon.server",
    "clientID": "<your mastodon client ID here>",
    "clientSecret": "<your mastodon client secret here>",
    "accessToken": "<your mastodon access token here>",
  }
  "wordsToPost": 10,
  "localOnly": false,
  "logPosts": true,
  "postInterval": "1h",
  "visibility": "unlisted"
}
```

The `visibility`, `logPosts`, and `localOnly` parameters are optional.

Launch the bot, and it should chug along reading posts from the Mastodon timeline, occasionally aggregating the posts it reads and writing a post.

## Attribution

The ignore list is loosely based off of Alir3z4's [stop-words](https://github.com/Alir3z4/stop-words) repository, with some additions and removals based on Fedi's speaking patterns ("mh" has removed, for instance, since mental health is regularly discussed under that acronym, and some common missing words like "people" have been added)
