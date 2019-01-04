package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/mattn/go-mastodon"
	"github.com/microcosm-cc/bluemonday"
	"html"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"
)

// The Config struct is the format for the configuration file (located at config.json).
type Config struct {
	Credentials  Credentials `json:"credentials"`
	LocalOnly    bool        `json:"localOnly"`
	LogPosts     bool        `json:"logposts"`
	PostInterval string      `json:"postInterval"`
	WordsToPost  int         `json:"wordsToPost"`
}

// Credentials holds the Mastodon credentials.
type Credentials struct {
	Server       string `json:"server"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
	AccessToken  string `json:"accessToken"`
	Visibility   string `json:"visibility"`
}

// WordList is a simple map type that stores each word and its number of occurrences.
type WordList map[string]int

const (
	trimchars = "!.,;?'`'\""
)

var (
	// Configuration file.
	config Config
	// Mastodon client struct, to control the bot session globally.
	client *mastodon.Client
	// Interval in which the aggregation function is run, compiled from the config.
	postInterval time.Duration
	// Timer for the aggregation function.
	timer *time.Timer
	// Bluemonday strip-tags policy, to avoid accidentally logging HTML tags.
	policy = bluemonday.StrictPolicy()
	// List of words currently being tracked..
	wordlist = make(WordList)
	// Number of posts sent this interval.
	postCount int
	// List of words that the WordList shouldn't ever track.
	ignoredWords []string
	// List of users that the WordList shouldn't ever track.
	blockedUsers []string
)

// Word is the structure used to represent a word and its occurrences, to sort the WordList.
type Word struct {
	Text  string
	Count int
}

func sortedList(list WordList) []Word {
	// convert the map into a slice
	wordSlice := make([]Word, len(wordlist))
	i := 0
	for k, v := range wordlist {
		wordSlice[i] = Word{k, v}
		i++
		// get rid of this now to free up memory
		delete(wordlist, k)
	}
	// sort the slice
	sort.Slice(wordSlice, func(i, j int) bool {
		return wordSlice[i].Count > wordSlice[j].Count
	})
	return wordSlice
}

func handleWord(status *mastodon.Status) {
	// ignore bot posts
	if status.Account.Bot {
		return
	}
	// ignore blocked users
	for _, user := range blockedUsers {
		if user == status.Account.Acct {
			return
		}
	}
	postCount++
	ignorecount := 0
	dupecount := 0
	var addedWords []string
	// strip HTML tags
	stripped := policy.Sanitize(status.Content)
	// convert it to lowercase
	lowered := strings.ToLower(stripped)
	// break into words
	words := strings.Split(lowered, " ")
	// process and add each word to the wordlist, if it is not a stop word
WordLoop:
	for _, word := range words {
		// unescape HTML entities
		word = html.UnescapeString(word)
		// trim the word
		word = strings.Trim(word, trimchars)
		// determine if the word is in the ignore list
		for _, ignoredWord := range ignoredWords {
			if ignoredWord == word {
				ignorecount++
				continue WordLoop
			}
		}
		// ensure the word is unique
		for _, addedWord := range addedWords {
			if addedWord == word {
				dupecount++
				continue WordLoop
			}
		}
		if len(word) > 0 {
			wordlist[word]++
			addedWords = append(addedWords, word)
		}
	}
	if config.LogPosts {
		log.Printf("Collected %d words (%d ignored, %d duplicate) from post by %s",
			len(words)-ignorecount-dupecount,
			ignorecount,
			dupecount,
			status.Account.Acct)
	}
}

func handleWSEvents(eventstream <-chan mastodon.Event) {
	for untypedEvent := range eventstream {
		switch evt := untypedEvent.(type) {
		case *mastodon.UpdateEvent:
			go handleWord(evt.Status)
		case *mastodon.ErrorEvent:
			// handle error
			log.Println("Error in timeline websocket:", evt)
			break
		default:
			continue
		}
	}
}

func aggregateposts() {
	log.Printf("Aggregation triggered. Total posts received: %d.\n", postCount)
	// reset the count now
	postCount = 0
	list := sortedList(wordlist)
	i := config.WordsToPost
	text := "Trending words on the Fediverse:"
	log.Println("Top 5 words:")
	for _, word := range list {
		i--
		if i < 0 {
			break
		}
		log.Printf("%s, posted %d times", word.Text, word.Count)
		text += fmt.Sprintf("\n- %s, posted %d times", word.Text, word.Count)
	}
	client.PostStatus(context.Background(), &mastodon.Toot{
		Status: text,
	})
	timer.Reset(postInterval)
}

func main() {
	log.Println("Reading list of ignored words...")
	// this is inefficient but if that becomes a problem i'll fix it later
	ignorefile, err := os.Open("ignore.txt")
	if err != nil {
		log.Fatal("Couldn't read ignored words list:", err)
	}
	scanner := bufio.NewScanner(ignorefile)
	for scanner.Scan() {
		ignoredWords = append(ignoredWords, scanner.Text())
	}
	log.Printf("%d ignored words loaded.\n", len(ignoredWords))
	blockfile, err := os.Open("block.txt")
	// only do this if the blockfile was found and there was no problem opening/reading it
	if err == nil {
		log.Println("Reading list of blocked users...")
		scanner := bufio.NewScanner(blockfile)
		for scanner.Scan() {
			blockedUsers = append(blockedUsers, scanner.Text())
		}
		log.Printf("%d blocked users loaded.\n", len(blockedUsers))
	}
	log.Println("Starting the bot...")
	configfile, err := os.Open("config.json")
	if err != nil {
		log.Fatal("Couldn't read config file:", err)
	}
	decoder := json.NewDecoder(configfile)
	err = decoder.Decode(&config)
	if err != nil {
		log.Fatal("Couldn't parse config file:", err)
	}
	postInterval, err = time.ParseDuration(config.PostInterval)
	if err != nil {
		log.Fatal("Couldn't parse duration:", err)
	}
	client = mastodon.NewClient(&mastodon.Config{
		Server:       config.Credentials.Server,
		ClientID:     config.Credentials.ClientID,
		ClientSecret: config.Credentials.ClientSecret,
		AccessToken:  config.Credentials.AccessToken,
	})
	wsclient := client.NewWSClient()
	ctx, cancel := context.WithCancel(context.Background())
	eventstream, err := wsclient.StreamingWSPublic(ctx, config.LocalOnly)
	if err != nil {
		log.Fatal("Couldn't open timeline websocket:", err)
	}
	log.Printf("Done. Entering event loop.")
	timer = time.AfterFunc(postInterval, aggregateposts)
	go handleWSEvents(eventstream)
	// wait for an interrupt signal
	sigchan := make(chan os.Signal)
	signal.Notify(sigchan, os.Interrupt, os.Kill)
	<-sigchan
	// cleanup
	log.Printf("Interrupt received, exiting...")
	cancel()
	os.Exit(0)
}
