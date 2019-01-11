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
	EnablePosts bool `json:"enablePosts"`
	Visibility   string      `json:"postVisibility"`
}

// Credentials holds the Mastodon credentials.
type Credentials struct {
	Server       string `json:"server"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
	AccessToken  string `json:"accessToken"`
}

// WordList is a simple map type that stores each word and its number of occurrences.
type WordList map[string]int

const (
	trimchars = "()[]{}!.,;?'`'\""
)

var (
	// Configuration file.
	config Config
	// Mastodon client struct, to control the bot session globally.
	client *mastodon.Client
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
			handleWord(evt.Status)
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
	if config.EnablePosts {
		client.PostStatus(context.Background(), &mastodon.Toot{
			Status: text,
		})
	}
}

func readLines(filepath string) (lines []string, err error) {
	// open file
	file, err := os.Open(filepath)
	if err != nil {
		return lines, err
	}
	// scan the file by line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, nil
}

func main() {
	var err error
	// read list of ignored words
	log.Println("Reading list of ignored words...")
	ignoredWords, err = readLines("ignore.txt")
	if err != nil {
		log.Fatal("Couldn't read ignore file:", err)
	}
	log.Printf("%d ignored words loaded.\n", len(ignoredWords))

	// read list of blocked users, if available
	blockedUsers, err = readLines("block.txt")
	if err != nil {
		log.Println("Blocked users file not found, continuing.")
	} else {
		log.Printf("%d blocked users loaded.\n", len(blockedUsers))
	}

	// read config file
	configfile, err := os.Open("config.json")
	if err != nil {
		log.Fatal("Couldn't read config file:", err)
	}
	decoder := json.NewDecoder(configfile)
	err = decoder.Decode(&config)
	if err != nil {
		log.Fatal("Couldn't parse config file:", err)
	}

	// log in to mastodon & set up websocket
	log.Println("Starting the bot...")
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
	// get post interval
	postInterval, err := time.ParseDuration(config.PostInterval)
	if err != nil {
		log.Fatal("Couldn't parse duration:", err)
	}
	// set up aggregation ticker
	ticker := time.NewTicker(postInterval)
	defer ticker.Stop()
	go func() {
		for {
			<-ticker.C
			aggregateposts()
		}
	}()

	// start event loop
	log.Printf("Done. Entering event loop.")
	go handleWSEvents(eventstream)
	// wait for an interrupt signal
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, os.Interrupt, os.Kill)
	<-sigchan
	// cleanup
	log.Printf("Interrupt received, exiting...")
	cancel()
	os.Exit(0)
}
