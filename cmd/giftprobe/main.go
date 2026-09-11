// Command giftprobe asks Telegram which gift methods this bot can actually
// reach, so the gift design rests on what the API allows rather than on what
// the documentation implies.
//
// It never sends a gift and never spends a Star. Read-only methods are called
// for real; every mutating method is called with no arguments at all, which is
// enough to tell "no such method" (404) from "method exists but wants
// parameters" (400) and nothing like enough to make it do anything.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// probe is one Bot API method worth knowing about.
type probe struct {
	method string
	// safe marks a read-only method that is called for real, with its result
	// printed. Everything else is an existence check only.
	safe bool
	why  string
}

var probes = []probe{
	{"getMe", true, "контроль: токен рабочий"},
	{"getMyStarBalance", true, "баланс звёзд бота, из него оплачиваются подарки"},
	{"getAvailableGifts", true, "каталог подарков, доступных боту к отправке"},

	{"sendGift", false, "выдать подарок победителю"},
	{"giftPremiumSubscription", false, "подарить Premium за звёзды"},

	{"getBusinessConnection", false, "есть ли вообще business-подключение"},
	{"getBusinessAccountGifts", false, "подарки управляемого business-аккаунта"},
	{"convertGiftToStars", false, "размен подарка обратно в звёзды"},
	{"upgradeGift", false, "апгрейд обычного подарка в уникальный"},
	{"transferGift", false, "передача уникального подарка"},

	{"getUserGifts", false, "подарки пользователя"},
	{"getChatGifts", false, "подарки чата"},

	{"racketkaDefinitelyNoSuchMethod", false, "контроль: заведомо несуществующий метод"},
}

type reply struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func main() {
	_ = godotenv.Load()

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "BOT_TOKEN не задан. Впишите его в .env и запустите снова.")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 15 * time.Second}
	results := make(map[string]reply, len(probes))

	fmt.Println("Опрашиваю Bot API. Ни один подарок не отправляется, ни одна звезда не тратится.")
	fmt.Println()
	fmt.Printf("%-32s %-34s %s\n", "МЕТОД", "ВЕРДИКТ", "ЗАЧЕМ")
	fmt.Println(strings.Repeat("-", 100))

	for _, p := range probes {
		r, err := call(ctx, client, token, p.method)
		if err != nil {
			fmt.Printf("%-32s %-34s %s\n", p.method, "ошибка запроса", sanitize(err.Error(), token))
			continue
		}
		results[p.method] = r
		fmt.Printf("%-32s %-34s %s\n", p.method, verdict(r), p.why)
	}

	fmt.Println()
	report(results)
}

func call(ctx context.Context, client *http.Client, token, method string) (reply, error) {
	url := "https://api.telegram.org/bot" + token + "/" + method

	// An empty JSON object: valid for the read-only methods, and short of every
	// required argument for the rest.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()

	var r reply
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return reply{}, fmt.Errorf("ответ не разобрался (HTTP %d): %w", resp.StatusCode, err)
	}
	return r, nil
}

func verdict(r reply) string {
	lower := strings.ToLower(r.Description)
	switch {
	case r.OK:
		return "ДОСТУПЕН"
	case r.ErrorCode == 404:
		return "нет такого метода"
	case r.ErrorCode == 401:
		return "токен отклонён"
	case strings.Contains(lower, "business"):
		return "есть, через business connection"
	case r.ErrorCode == 400:
		return "есть, нужны параметры"
	case r.ErrorCode == 403:
		return "запрещён боту"
	default:
		return fmt.Sprintf("код %d", r.ErrorCode)
	}
}

// report prints what the read-only calls actually returned, which is the part
// the gift catalogue will be built from.
func report(results map[string]reply) {
	if r, ok := results["getMe"]; ok && r.OK {
		var me struct {
			Username string `json:"username"`
			ID       int64  `json:"id"`
		}
		if json.Unmarshal(r.Result, &me) == nil {
			fmt.Printf("Бот: @%s (id %d)\n", me.Username, me.ID)
		}
	}

	if r, ok := results["getMyStarBalance"]; ok {
		if r.OK {
			var bal struct {
				Amount int64 `json:"amount"`
			}
			if json.Unmarshal(r.Result, &bal) == nil {
				fmt.Printf("Баланс звёзд бота: %d\n", bal.Amount)
			}
		} else {
			fmt.Printf("Баланс звёзд недоступен: %s\n", r.Description)
		}
	}

	r, ok := results["getAvailableGifts"]
	if !ok || !r.OK {
		fmt.Println("Каталог подарков недоступен — фаза выдачи подарков пока не строится.")
		return
	}

	var catalogue struct {
		Gifts []struct {
			ID               string `json:"id"`
			StarCount        int64  `json:"star_count"`
			UpgradeStarCount int64  `json:"upgrade_star_count"`
			TotalCount       int64  `json:"total_count"`
			RemainingCount   int64  `json:"remaining_count"`
		} `json:"gifts"`
	}
	if err := json.Unmarshal(r.Result, &catalogue); err != nil {
		fmt.Printf("Каталог не разобрался: %v\n", err)
		return
	}

	fmt.Printf("\nКаталог подарков: %d штук\n", len(catalogue.Gifts))

	prices := make([]int64, 0, len(catalogue.Gifts))
	limited := 0
	for _, g := range catalogue.Gifts {
		prices = append(prices, g.StarCount)
		if g.TotalCount > 0 {
			limited++
		}
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })

	if len(prices) > 0 {
		fmt.Printf("Цены в звёздах: от %d до %d\n", prices[0], prices[len(prices)-1])
		fmt.Printf("Номиналы: %s\n", joinPrices(prices))
	}
	fmt.Printf("Лимитированных (с ограниченным тиражом): %d\n", limited)
	fmt.Println("\nЭти номиналы и станут ступенями выдачи выигрыша подарком.")
}

func joinPrices(prices []int64) string {
	seen := map[int64]bool{}
	parts := make([]string, 0, len(prices))
	for _, p := range prices {
		if seen[p] {
			continue
		}
		seen[p] = true
		parts = append(parts, fmt.Sprint(p))
	}
	return strings.Join(parts, ", ")
}

// sanitize keeps the token out of anything printed: net/http folds the request
// URL, token and all, into its error strings.
func sanitize(s, token string) string {
	return strings.ReplaceAll(s, token, "<TOKEN>")
}
