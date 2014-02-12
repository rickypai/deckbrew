package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/codegangsta/martini"
	"github.com/codegangsta/martini-contrib/gzip"
	_ "github.com/lib/pq"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func GetHostname() string {
	hostname := os.Getenv("DECKBREW_HOSTNAME")

	if hostname == "" {
		return "http://localhost:3000"
	}

	return hostname
}

func ReverseCard(id string) string {
	return fmt.Sprintf("%s/mtg/cards/%s", GetHostname(), id)
}

func ReverseEdition(id int) string {
	return fmt.Sprintf("%s/mtg/cards?multiverseid=%d", GetHostname(), id)
}

func ReverseSet(id string) string {
	return fmt.Sprintf("%s/mtg/sets/%s", GetHostname(), id)
}

func MTGImageURL(id int) string {
	return fmt.Sprintf("http://mtgimage.com/multiverseid/%d.jpg", id)
}

func Slug(name string) string {
	re := regexp.MustCompile(`[,.'"?:()]`)
	d := strings.Replace(strings.ToLower(name), " ", "-", -1)
	return re.ReplaceAllLiteralString(d, "")
}

// Import this eventually
type Card struct {
	Name          string            `json:"name"`
	Id            string            `json:"id"`
	Href          string            `json:"url,omitempty"`
	StoreUrl      string            `json:"store_url"`
	Types         []string          `json:"types,omitempty"`
	Supertypes    []string          `json:"supertypes,omitempty"`
	Subtypes      []string          `json:"subtypes,omitempty"`
	Colors        []string          `json:"colors,omitempty"`
	ConvertedCost int               `json:"cmc"`
	ManaCost      string            `json:"cost"`
	Text          string            `json:"text"`
	Power         string            `json:"power,omitempty"`
	Toughness     string            `json:"toughness,omitempty"`
	Loyalty       int               `json:"loyalty,omitempty"`
	FormatMap     map[string]string `json:"formats"`
	Editions      []Edition         `json:"editions,omitempty"`
}

func (c *Card) Sets() []string {
	sets := []string{}
	for _, e := range c.Editions {
		sets = append(sets, e.SetId)
	}
	return ToUniqueLower(sets)
}

func (c *Card) Formats() []string {
	v := []string{}
	for format, _ := range c.FormatMap {
		v = append(v, format)
	}
	return ToUniqueLower(v)
}

func (c *Card) Status() []string {
	v := []string{}
	for _, status := range c.FormatMap {
		v = append(v, status)
	}
	return ToUniqueLower(v)
}

func (c *Card) Rarities() []string {
	r := []string{}
	for _, e := range c.Editions {
		r = append(r, e.Rarity)
	}
	return ToUniqueLower(r)
}

func (c *Card) MultiverseIds() []string {
	r := []string{}
	for _, e := range c.Editions {
		r = append(r, strconv.Itoa(e.MultiverseId))
	}
	return ToUniqueLower(r)
}

func (c *Card) Multicolor() bool {
	return len(c.Colors) > 1
}

func (c *Card) Fill() {
	c.Href = ReverseCard(c.Id)
	c.StoreUrl = TCGCardURL(c)

	for i, _ := range c.Editions {
		e := &c.Editions[i]
		e.Href = ReverseEdition(e.MultiverseId)
		e.SetUrl = ReverseSet(e.SetId)
		e.ImageUrl = MTGImageURL(e.MultiverseId)
		e.StoreUrl = TCGEditionURL(c, e)
	}
}

type Edition struct {
	Set          string `json:"set"`
	SetId        string `json:"set_id"`
	CardId       string `json:"-"`
	Watermark    string `json:"watermark,omitempty"`
	Rarity       string `json:"rarity"`
	Border       string `json:"-"`
	Artist       string `json:"artist"`
	MultiverseId int    `json:"multiverse_id"`
	Flavor       string `json:"flavor,omitempty"`
	Number       string `json:"number"`
	Layout       string `json:"layout"`
	Price        Price  `json:"price,omitempty"`
	Href         string `json:"url,omitempty"`
	ImageUrl     string `json:"image_url,omitempty"`
	SetUrl       string `json:"set_url,omitempty"`
	StoreUrl     string `json:"store_url"`
}

type Price struct {
	Low     int    `json:"low"`
	Average int    `json:"average"`
	Foil    int    `json:"average_foil"`
	High    int    `json:"high"`
	Note    string `json:"note"`
}

type Set struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Border   string `json:"border"`
	Type     string `json:"type"`
	Href     string `json:"url"`
	CardsUrl string `json:"cards_url"`
}

func (s *Set) Fill() {
	s.Href = fmt.Sprintf("%s/mtg/sets/%s", GetHostname(), s.Id)
	s.CardsUrl = fmt.Sprintf("%s/mtg/cards?set=%s", GetHostname(), s.Id)
}

func JSON(code int, val interface{}) (int, []byte) {
	blob, err := json.Marshal(val)

	if err != nil {
		return 500, []byte(`{"error": "Internal server error :("}"`)
	}

	return code, blob
}

func Errors(errors ...string) ApiError {
	return ApiError{Errors: errors}
}

func LinkHeader(host string, u *url.URL, page int) string {
	if page == 0 {
		qstring := u.Query()
		qstring.Set("page", "1")
		return fmt.Sprintf("<%s%s?%s>; rel=\"next\"", host, u.Path, qstring.Encode())
	} else {
		qstring := u.Query()

		qstring.Set("page", strconv.Itoa(page-1))
		prev := fmt.Sprintf("<%s%s?%s>; rel=\"prev\"", host, u.Path, qstring.Encode())

		qstring.Set("page", strconv.Itoa(page+1))
		next := fmt.Sprintf("<%s%s?%s>; rel=\"next\"", host, u.Path, qstring.Encode())

		return prev + ", " + next
	}
}

type ApiError struct {
	Errors []string `json:"errors"`
}

func HandleCards(db *sql.DB, req *http.Request, w http.ResponseWriter) (int, []byte) {
	cond, err, errors := ParseSearch(req.URL)

	if err != nil {
		log.Println(err)
		return JSON(http.StatusBadRequest, Errors(errors...))
	}

	cards, err := FetchCards(db, cond)

	if err != nil {
		log.Println(err)
		return JSON(http.StatusNotFound, Errors("Cards not found"))
	}

	w.Header().Set("Link", LinkHeader(GetHostname(), req.URL, 0))

	return JSON(http.StatusOK, cards)
}

func HandleCard(db *sql.DB, params martini.Params) (int, []byte) {
	card, err := FetchCard(db, params["id"])

	if err != nil {
		log.Println(err)
		return JSON(http.StatusNotFound, Errors("Card not found"))
	}

	return JSON(http.StatusOK, card)
}

func HandleSets(db *sql.DB) (int, []byte) {
	sets, err := FetchSets(db)

	if err != nil {
		log.Println(err)
		return JSON(http.StatusNotFound, Errors("Sets not found"))
	}

	return JSON(http.StatusOK, sets)
}

func HandleSet(db *sql.DB, params martini.Params) (int, []byte) {
	card, err := FetchSet(db, params["id"])

	if err != nil {
		log.Println(err)
		return JSON(http.StatusNotFound, Errors("Set not found"))
	}

	return JSON(http.StatusOK, card)
}

func HandleTerm(term string) func(*sql.DB) (int, []byte) {
	return func(db *sql.DB) (int, []byte) {
		terms, err := FetchTerms(db, term)

		if err != nil {
			log.Println(err)
			return JSON(http.StatusNotFound, Errors(term+" not found"))
		}

		return JSON(http.StatusOK, terms)
	}
}

func HandleTypeahead(db *sql.DB, req *http.Request) (int, []byte) {
	cards, err := FetchTypeahead(db, req.URL.Query().Get("q"))

	if err != nil {
		log.Println(err)
		return JSON(http.StatusNotFound, Errors(" Can't find any cards that match that search"))
	}

	return JSON(http.StatusOK, cards)
}

type Pong struct {
	Rally string `json:"rally"`
}

func Ping(db *sql.DB) (int, []byte) {
	if db.Ping() != nil {
		return JSON(http.StatusInternalServerError, Errors("The database could not be reached"))
	}

	return JSON(http.StatusOK, Pong{Rally: "serve"})
}

func NotFound() (int, []byte) {
	return JSON(http.StatusNotFound, Errors("No endpoint here"))
}

func NewApi() *martini.Martini {
	m := martini.New()

	// Setup middleware
	m.Use(martini.Recovery())
	m.Use(martini.Logger())
	m.Use(gzip.All())
	m.Use(func(c martini.Context, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public,max-age=3600")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("License", "The textual information presented through this API about Magic: The Gathering is copyrighted by Wizards of the Coast.")
		w.Header().Set("Disclaimer", "This API is not produced, endorsed, supported, or affiliated with Wizards of the Coast.")
	})

	r := martini.NewRouter()

	r.Get("/ping", Ping)
	r.Get("/mtg/cards", HandleCards)
	r.Get("/mtg/cards/typeahead", HandleTypeahead)
	r.Get("/mtg/cards/:id", HandleCard)
	r.Get("/mtg/sets", HandleSets)
	r.Get("/mtg/sets/:id", HandleSet)
	r.Get("/mtg/colors", HandleTerm("colors"))
	r.Get("/mtg/supertypes", HandleTerm("supertypes"))
	r.Get("/mtg/subtypes", HandleTerm("subtypes"))
	r.Get("/mtg/types", HandleTerm("types"))
	r.NotFound(NotFound)

	m.Action(r.Handle)
	return m
}

func main() {
	flag.Parse()

	if flag.Arg(0) == "load" {
		err := SyncDatabase(flag.Arg(1))

		if err != nil {
			log.Fatal(err)
		}

		log.Println("Loaded all data into the database")
		return
	}

	if flag.Arg(0) == "dump" {
		err := DumpDatabase(flag.Arg(1), flag.Arg(2))

		if err != nil {
			log.Fatal(err)
		}

		log.Println("Loaded all data into the database")
		return
	}

	db, err := GetDatabase()

	//NewMain(db)
	//return

	if err != nil {
		log.Fatal(err)
	}

	m := NewApi()
	m.Map(db)
	m.Run()
}
