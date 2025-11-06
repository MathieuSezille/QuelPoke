package main

import (
	"crypto/sha1"
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed index.tmpl.html
var indexTemplateFS embed.FS

type indexTemplateParams struct {
	PokemonID   uint64
	PokemonName string
	Stats       []Stat
	RadarPoints string
	Evolutions  []Evolution
	Name        string
	Version     string
}

// Stat represents a single base stat from the PokeAPI
type Stat struct {
	Name    string
	Base    int
	Percent int
}

type Evolution struct {
	Name  string
	ID    uint64
	Image string
}

// env return environment value or default if not exists
func env(name string, def string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return def
}

func main() {
	addr := env("ADDR", "0.0.0.0")
	port := env("PORT", "8080")
	listen := fmt.Sprintf("%s:%s", addr, port)

	log.Printf("starting quelpoke app on http://%s", listen)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", index)
	if err := http.ListenAndServe(listen, mux); err != nil {
		log.Fatal("failed to listen and serve:", err)
	}
}

// index renders the index template. It takes name in query parameters
func index(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	name := r.URL.Query().Get("name")
	tmpl, err := template.New("").ParseFS(indexTemplateFS, "*")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("[ERR] failed to parse embed fs:", err)
		return
	}

	params := indexTemplateParams{
		PokemonID: pokemonID(name, 151),
		Name:      name,
		Version:   env("VERSION", "dev"),
	}
	params.PokemonName, err = pokemonName(params.PokemonID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("[ERR] failed to get pokemon name:", err)
		return
	}

	// fetch pokemon stats (non-fatal)
	if stats, err := pokemonStats(params.PokemonID); err != nil {
		log.Println("[WARN] failed to fetch pokemon stats:", err)
		params.Stats = nil
	} else {
		params.Stats = stats
	}

	// compute radar polygon points for stats (for SVG)
	params.RadarPoints = radarPath(params.Stats)

	// fetch evolution chain (non-fatal)
	if evs, err := pokemonEvolutions(params.PokemonID); err != nil {
		log.Println("[WARN] failed to fetch evolution chain:", err)
		params.Evolutions = nil
	} else {
		params.Evolutions = evs
	}

	if err := tmpl.ExecuteTemplate(w, "index.tmpl.html", params); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Println("[ERR] failed to execute index template:", err)
		return
	}

	log.Printf("generated page in %s with pokemon id: %d for name: %s", time.Since(start).String(), params.PokemonID, params.Name)
}

// pokemonID computes the sha1 sum of the name and return
// the modulo by m (m is the maximum pokemon id)
func pokemonID(name string, m uint64) uint64 {
	hasher := sha1.New()
	hasher.Write([]byte(name))
	return binary.BigEndian.Uint64(hasher.Sum(nil))%m + 1
}

func pokemonName(id uint64) (string, error) {
	// Get French name from species endpoint
	req, err := http.NewRequest("GET", fmt.Sprintf("https://pokeapi.co/api/v2/pokemon-species/%d", id), nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var speciesData struct {
		Names []struct {
			Name     string `json:"name"`
			Language struct {
				Name string `json:"name"`
			} `json:"language"`
		} `json:"names"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&speciesData); err != nil {
		return "", err
	}

	// Find French name
	for _, n := range speciesData.Names {
		if n.Language.Name == "fr" {
			return n.Name, nil
		}
	}

	// Fallback to default name from pokemon endpoint if French not found
	req2, err := http.NewRequest("GET", fmt.Sprintf("https://pokeapi.co/api/v2/pokemon/%d", id), nil)
	if err != nil {
		return "", err
	}

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()

	var pokemon struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(resp2.Body).Decode(&pokemon); err != nil {
		return "", err
	}

	return pokemon.Name, nil
}

// pokemonStats fetches base stats for the given pokemon id from pokeapi
func pokemonStats(id uint64) ([]Stat, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://pokeapi.co/api/v2/pokemon/%d", id), nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data struct {
		Stats []struct {
			Base int `json:"base_stat"`
			Stat struct {
				Name string `json:"name"`
			} `json:"stat"`
		} `json:"stats"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	out := make([]Stat, 0, len(data.Stats))
	for _, s := range data.Stats {
		percent := 0
		if s.Base > 0 {
			percent = s.Base * 100 / 255
		}
		out = append(out, Stat{Name: s.Stat.Name, Base: s.Base, Percent: percent})
	}
	return out, nil
}

// radarPath builds an SVG points string for a polygon representing the stats
func radarPath(stats []Stat) string {
	if len(stats) == 0 {
		return ""
	}
	centerX := 60.0
	centerY := 60.0
	maxR := 45.0
	parts := make([]string, 0, len(stats))
	for i, s := range stats {
		angle := -math.Pi/2 + 2*math.Pi*float64(i)/float64(len(stats))
		r := float64(s.Percent) / 100.0 * maxR
		x := centerX + r*math.Cos(angle)
		y := centerY + r*math.Sin(angle)
		parts = append(parts, fmt.Sprintf("%.2f,%.2f", x, y))
	}
	return strings.Join(parts, " ")
}

// pokemonEvolutions fetches the evolution chain (simple first-branch traversal)
func pokemonEvolutions(id uint64) ([]Evolution, error) {
	// fetch species to get evolution chain url
	req, err := http.NewRequest("GET", fmt.Sprintf("https://pokeapi.co/api/v2/pokemon-species/%d", id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var speciesData struct {
		EvolutionChain struct {
			URL string `json:"url"`
		} `json:"evolution_chain"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&speciesData); err != nil {
		return nil, err
	}

	// fetch evolution chain
	req2, err := http.NewRequest("GET", speciesData.EvolutionChain.URL, nil)
	if err != nil {
		return nil, err
	}
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()

	var chainData struct {
		Chain struct {
			Species struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"species"`
			EvolvesTo []struct {
				Species struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"species"`
				EvolvesTo []interface{} `json:"evolves_to"`
			} `json:"evolves_to"`
		} `json:"chain"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&chainData); err != nil {
		return nil, err
	}

	// traverse first-branch path
	var out []Evolution
	node := chainData.Chain
	for {
		sp := node.Species
		// extract id from url (last part)
		u := strings.TrimRight(sp.URL, "/")
		parts := strings.Split(u, "/")
		var spid uint64
		if len(parts) > 0 {
			if v, err := strconv.ParseUint(parts[len(parts)-1], 10, 64); err == nil {
				spid = v
			}
		}
		img := ""
		if spid > 0 {
			img = fmt.Sprintf("https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/other/official-artwork/%d.png", spid)
		}
		out = append(out, Evolution{Name: sp.Name, ID: spid, Image: img})

		if len(node.EvolvesTo) == 0 {
			break
		}
		// move to first evolves_to
		// decode node.EvolvesTo[0] into the same structure by re-marshalling—simpler is to unmarshal into a temporary struct
		var next struct {
			Species struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"species"`
			EvolvesTo []struct {
				Species struct {
					Name string `json:"name"`
					URL  string `json:"url"`
				} `json:"species"`
				EvolvesTo []interface{} `json:"evolves_to"`
			} `json:"evolves_to"`
		}
		// marshal/unmarshal via map is avoided; use a quick json roundtrip from raw interface
		// easier: build next from node.EvolvesTo[0] by converting with json
		b, _ := json.Marshal(node.EvolvesTo[0])
		if err := json.Unmarshal(b, &next); err != nil {
			break
		}
		// assign node = next (need to convert shape)
		node.Species = next.Species
		node.EvolvesTo = next.EvolvesTo
	}

	return out, nil
}
