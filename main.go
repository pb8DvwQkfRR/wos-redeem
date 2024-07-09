package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	BASE_URL       = "https://wos-giftcode-api.centurygame.com/api"
	SECRET         = "tB87#kPtkxqOS2"
	MAX_RETRIES    = 20
	RETRY_DELAY    = 8 * time.Second
	DELAY_DURATION = 2 * time.Second
)

type PlayerResponse struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Msg     string          `json:"msg"`
	ErrCode interface{}     `json:"err_code"`
}

type Player struct {
	FID            int         `json:"fid"`
	Nickname       string      `json:"nickname"`
	KID            int         `json:"kid"`
	StoveLv        interface{} `json:"stove_lv"`
	StoveLvContent interface{} `json:"stove_lv_content"`
	AvatarImage    string      `json:"avatar_image"`
}

type ExchangeResponse struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Msg     string          `json:"msg"`
	ErrCode int             `json:"err_code"`
}

func main() {
	token := os.Getenv("DC_TOKEN")
	if token == "" {
		fmt.Println("Discord bot token is not set in the environment variables")
		return
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("Error creating Discord session:", err)
		return
	}

	dg.AddHandler(ready)
	dg.AddHandler(messageCreate)

	err = dg.Open()
	if err != nil {
		fmt.Println("Error opening connection:", err)
		return
	}

	_, err = dg.ApplicationCommandCreate(dg.State.User.ID, "", &discordgo.ApplicationCommand{
		Name:        "redeem",
		Description: "Redeem code",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "code",
				Description: "Gift Code",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "id",
				Description: "User ID(optional, default for R69)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
			},
		},
	})
	if err != nil {
		fmt.Println("Error creating slash command:", err)
		return
	}

	_, err = dg.ApplicationCommandCreate(dg.State.User.ID, "", &discordgo.ApplicationCommand{
		Name:        "help",
		Description: "Get help information about the bot",
	})
	if err != nil {
		fmt.Println("Error creating help slash command:", err)
		return
	}

	dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type == discordgo.InteractionApplicationCommand {
			handleSlashCommand(s, i)
		}
	})

	fmt.Println("Bot is now running. Press CTRL-C to exit.")
	select {}
}

func ready(s *discordgo.Session, event *discordgo.Ready) {
	fmt.Println("Bot is ready!")
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if strings.HasPrefix(m.Content, "/redeem ") {
		parts := strings.Fields(m.Content)
		if len(parts) == 2 {
			go handleRedemption(s, m, nil, parts[1], "")
		} else if len(parts) == 3 {
			go handleRedemption(s, m, nil, parts[1], parts[2])
		} else {
			s.ChannelMessageSend(m.ChannelID, "Invalid command. Use '/redeem code' or '/redeem code ID'")
		}
	}
}

func handleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.ApplicationCommandData().Name {
	case "redeem":
		handleRedeemCommand(s, i)
	case "help":
		handleHelpCommand(s, i)
	}
}

func handleRedeemCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	var code string
	var id string

	for _, option := range options {
		switch option.Name {
		case "code":
			code = option.StringValue()
		case "id":
			id = strconv.FormatInt(option.IntValue(), 10)
		}
	}

	go handleRedemption(s, nil, i, code, id)
}

func handleHelpCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	helpMessage := "Here's how to use the bot:\n\n" +
		"1. To redeem a code for R69: `/redeem <code>`\n\n" +
		"2. To redeem a code for a specific user: `/redeem <code> <user_id>`\n\n" +
		"If you have any other questions/advices, please contact <@693703407780888636>"

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: helpMessage,
			Flags:   1 << 6,
		},
	})
}

func handleRedemption(s *discordgo.Session, m *discordgo.MessageCreate, i *discordgo.InteractionCreate, code string, specificID string) {
	userIDs, err := readUserIDs("./ids")
	if err != nil {
		sendErrorMessage(s, m, i, fmt.Sprintf("Error reading user IDs: %v", err))
		return
	}

	if specificID != "" {
		userIDs = []string{specificID}
	}

	msg := sendInitialMessage(s, m, i, code)

	results := make([]string, 0, len(userIDs))
	updateMessage := createUpdateMessageFunc(s, m, i, msg, code, &results)

	for _, userID := range userIDs {
		processUser(userID, code, &results, updateMessage)
		time.Sleep(DELAY_DURATION)
	}

	updateMessage() // Send final update
}

func sendErrorMessage(s *discordgo.Session, m *discordgo.MessageCreate, i *discordgo.InteractionCreate, content string) {
	if i != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
				Flags:   1 << 6,
			},
		})
	} else {
		s.ChannelMessageSend(m.ChannelID, content)
	}
}

func sendInitialMessage(s *discordgo.Session, m *discordgo.MessageCreate, i *discordgo.InteractionCreate, code string) *discordgo.Message {
	content := "Processing gift code: **" + code + "**"
	if i != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
				Flags:   1 << 6,
			},
		})
		return nil
	}

	msg, err := s.ChannelMessageSend(m.ChannelID, content)
	if err != nil {
		fmt.Println("Error sending message:", err)
	}
	return msg
}

func createUpdateMessageFunc(s *discordgo.Session, m *discordgo.MessageCreate, i *discordgo.InteractionCreate, msg *discordgo.Message, code string, results *[]string) func() {
	return func() {
		content := fmt.Sprintf("Processing gift code: **%s**\n\n%s", code, strings.Join(*results, "\n"))
		if i != nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
		} else if msg != nil {
			_, err := s.ChannelMessageEdit(m.ChannelID, msg.ID, content)
			if err != nil {
				fmt.Println("Error updating message:", err)
			}
		}
	}
}

func processUser(userID, code string, results *[]string, updateMessage func()) {
	playerData, err := retryRequest(func() (*Player, error) {
		return getRoleInfo(userID)
	})
	if err != nil {
		status := handlePlayerInfoError(err, userID)
		*results = append(*results, status)
		updateMessage()
		return
	}

	exchangeData, err := exchangeCode(userID, code)
	if err != nil {
		status := handleExchangeError(err, playerData.Nickname)
		*results = append(*results, status)
	} else {
		status := getStatus(exchangeData.Msg)
		*results = append(*results, fmt.Sprintf("%s - %s", playerData.Nickname, status))
	}

	updateMessage()
}

func handlePlayerInfoError(err error, userID string) string {
	if strings.Contains(err.Error(), "role not exist") {
		return fmt.Sprintf("%s - ❌ USER NOT FOUND", userID)
	}
	return fmt.Sprintf("Error getting player info for %s: %v", userID, err)
}

func handleExchangeError(err error, nickname string) string {
	if strings.Contains(err.Error(), "RECEIVED") {
		return fmt.Sprintf("%s - ❗️ %s", nickname, err.Error())
	}
	return fmt.Sprintf("%s - ❌ %v", nickname, err)
}

func getStatus(msg string) string {
	switch msg {
	case "RECEIVED":
		return "❗️"
	case "SUCCESS":
		return "✅"
	case "CDK NOT FOUND":
		return "❌"
	default:
		return "❓"
	}
}

func readUserIDs(filename string) ([]string, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(content), "\n")
	var ids []string
	for _, line := range lines {
		id := strings.TrimSpace(line)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func getRoleInfo(userID string) (*Player, error) {
	data := url.Values{}
	data.Set("fid", userID)
	data.Set("time", strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10))

	signedData := appendSign(data)
	resp, err := http.PostForm(BASE_URL+"/player", signedData)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var playerResp PlayerResponse
	err = json.Unmarshal(body, &playerResp)
	if err != nil {
		return nil, fmt.Errorf("JSON unmarshal error: %v", err)
	}

	if playerResp.Code != 0 {
		return nil, fmt.Errorf("API error: %s (Code: %d, ErrCode: %v)", playerResp.Msg, playerResp.Code, playerResp.ErrCode)
	}

	var player Player
	err = json.Unmarshal(playerResp.Data, &player)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse player data: %v", err)
	}

	return &player, nil
}

func exchangeCode(userID, code string) (*ExchangeResponse, error) {
	data := url.Values{}
	data.Set("fid", userID)
	data.Set("cdk", code)
	data.Set("time", strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10))

	signedData := appendSign(data)
	resp, err := http.PostForm(BASE_URL+"/gift_code", signedData)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var exchangeResp ExchangeResponse
	err = json.Unmarshal(body, &exchangeResp)
	if err != nil {
		return nil, fmt.Errorf("JSON unmarshal error: %v", err)
	}

	if exchangeResp.Code != 0 {
		return &exchangeResp, fmt.Errorf("%s", exchangeResp.Msg)
	}

	return &exchangeResp, nil
}

func appendSign(data url.Values) url.Values {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	for _, k := range keys {
		if buf.Len() > 0 {
			buf.WriteByte('&')
		}
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(data.Get(k))
	}
	buf.WriteString(SECRET)

	hash := md5.Sum([]byte(buf.String()))
	sign := hex.EncodeToString(hash[:])

	data.Set("sign", sign)
	return data
}

func retryRequest[T any](fn func() (*T, error)) (*T, error) {
	var (
		err     error
		result  *T
		backoff = RETRY_DELAY
	)

	for attempt := 0; attempt < MAX_RETRIES; attempt++ {
		result, err = fn()
		if err == nil {
			return result, nil
		}

		if strings.Contains(err.Error(), "Too Many Attempts") ||
			strings.Contains(err.Error(), "invalid character '<' looking for beginning of value") ||
			strings.Contains(err.Error(), "Sign Error") {
			fmt.Printf("Received error. Retrying in %v. Attempt %d/%d\n", backoff, attempt+1, MAX_RETRIES)
			time.Sleep(backoff)
			backoff += time.Second
		} else {
			return nil, err
		}
	}

	return nil, fmt.Errorf("max retries reached: %w", err)
}
