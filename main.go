package main

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/mattn/go-mastodon"
	"github.com/microcosm-cc/bluemonday"
	"html"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

// The Config struct is the format for the configuration file (located at config.json).
type Config struct {
	Server       string `json:"server"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
	AccessToken  string `json:"accessToken"`
	LocalOnly    bool   `json:"localOnly"`
	PostInterval string `json:"postInterval"`
}

// WordList is a simple map type that stores each word and its number of occurrences.
type WordList map[string]int

const (
	trimchars = "!.,;?"
)

var (
	// Configuration file.
	config Config
	// Interval in which the aggregation function is run.
	postInterval time.Duration
	// Timer for the aggregation function.
	timer *time.Timer
	// Bluemonday strip-tags policy, to avoid accidentally logging HTML tags.
	policy = bluemonday.StrictPolicy()
	// List of words currently being tracked..
	wordlist = make(WordList)
	// Number of toots sent this interval.
	tootCount int
	// List of stop words that the WordList shouldn't ever carry.
	stopWords []string
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

func handleWSEvents(eventstream <-chan mastodon.Event) {
	for untypedEvent := range eventstream {
		switch evt := untypedEvent.(type) {
		case *mastodon.UpdateEvent:
			tootCount++
			stopwordcount := 0
			// strip HTML tags
			stripped := policy.Sanitize(evt.Status.Content)
			// unescape HTML entities
			unescaped := html.UnescapeString(stripped)
			// break into words
			words := strings.Split(unescaped, " ")
			// process and add each word to the wordlist, if it is not a stop word
		WordLoop:
			for i := range words {
				word := strings.Trim(words[i], " ")
				word = strings.ToLower(word)
				found := false
				for _, stopWord := range stopWords {
					if stopWord == word {
						stopwordcount++
						found = true
						continue WordLoop
					}
				}
				if !found {
					wordlist[word]++
				}
			}
			log.Printf("Collected %d words (%d stop words omitted) from toot by %s", len(words), stopwordcount, evt.Status.Account.Username)
		case *mastodon.ErrorEvent:
			// handle error
			log.Println("Error in timeline websocket:", evt)
			break
		default:
			continue
		}
	}
}

func aggregateToots() {
	log.Printf("Aggregation triggered. Total toots received: %d.\n", tootCount)
	// reset the count now
	tootCount = 0
	list := sortedList(wordlist)
	i := 5
	log.Println("Top 5 words:")
	for _, word := range list {
		i--
		if i < 0 {
			break
		}
		log.Printf("%s, tooted %d times", word.Text, word.Count)
	}
	timer.Reset(postInterval)
}

func main() {
	log.Println("Reading list of stop words...")
	// this is inefficient but if that becomes a problem i'll fix it later
	stopfile, err := os.Open("stopwords.txt")
	if err != nil {
		log.Fatal("Couldn't read stop words list:", err)
	}
	scanner := bufio.NewScanner(stopfile)
	for scanner.Scan() {
		stopWords = append(stopWords, scanner.Text())
	}
	log.Printf("%d stop words loaded.\n", len(stopWords))
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
	client := mastodon.NewClient(&mastodon.Config{
		Server:       config.Server,
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		AccessToken:  config.AccessToken,
	})
	wsclient := client.NewWSClient()
	eventstream, err := wsclient.StreamingWSPublic(context.Background(), config.LocalOnly)
	if err != nil {
		log.Fatal("Couldn't open timeline websocket:", err)
	}
	log.Printf("Done. Entering event loop.")
	timer = time.AfterFunc(postInterval, aggregateToots)
	handleWSEvents(eventstream)
}
