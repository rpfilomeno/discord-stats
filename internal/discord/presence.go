package discord

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Discord's own limits; longer values are dropped by the gateway.
const maxPresenceText = 128

// PresenceFromEnv builds the account presence sent with IDENTIFY, so the
// account is never briefly "online with nothing set" before a later update.
//
//	STATUS                online | idle | dnd | invisible
//	CUSTOM_STATUS         custom status text
//	CUSTOM_STATUS_EMOJI   unicode emoji shown before the text
//	RPC_NAME              activity name ("Playing <name>"); empty disables it
//	RPC_TYPE              playing | listening | watching | competing
//	RPC_DETAILS           first line under the name
//	RPC_STATE             second line
//	RPC_IMAGE             asset key, or a URL when RPC_APP_ID is set
//	RPC_IMAGE_TEXT        hover text for the image
//	RPC_START_MIN         minutes ago the activity "started" (elapsed timer)
//	RPC_APP_ID            application id that owns the image asset
func PresenceFromEnv() *discordgo.GatewayStatusUpdate {
	p := &discordgo.GatewayStatusUpdate{Status: statusFromEnv()}

	if text := strings.TrimSpace(os.Getenv("CUSTOM_STATUS")); text != "" {
		act := &discordgo.Activity{
			Name:      "Custom Status",
			Type:      discordgo.ActivityTypeCustom,
			State:     cut(text),
			CreatedAt: time.Now(),
		}
		if e := strings.TrimSpace(os.Getenv("CUSTOM_STATUS_EMOJI")); e != "" {
			act.Emoji = discordgo.Emoji{Name: cut(e)}
		}
		p.Activities = append(p.Activities, act)
	}

	if name := strings.TrimSpace(os.Getenv("RPC_NAME")); name != "" {
		act := &discordgo.Activity{
			Name:          cut(name),
			Type:          activityType(os.Getenv("RPC_TYPE")),
			Details:       cut(strings.TrimSpace(os.Getenv("RPC_DETAILS"))),
			State:         cut(strings.TrimSpace(os.Getenv("RPC_STATE"))),
			ApplicationID: strings.TrimSpace(os.Getenv("RPC_APP_ID")),
			CreatedAt:     time.Now(),
		}
		if img := strings.TrimSpace(os.Getenv("RPC_IMAGE")); img != "" {
			act.Assets = discordgo.Assets{
				LargeImageID: img,
				LargeText:    cut(strings.TrimSpace(os.Getenv("RPC_IMAGE_TEXT"))),
			}
		}
		if m, err := strconv.Atoi(os.Getenv("RPC_START_MIN")); err == nil && m > 0 {
			act.Timestamps = discordgo.TimeStamps{
				StartTimestamp: time.Now().Add(-time.Duration(m) * time.Minute).UnixMilli(),
			}
		}
		p.Activities = append(p.Activities, act)
	}
	return p
}

func statusFromEnv() string {
	switch s := strings.ToLower(strings.TrimSpace(os.Getenv("STATUS"))); s {
	case "online", "idle", "dnd", "invisible":
		return s
	case "":
		return "online"
	}
	return "online"
}

func activityType(s string) discordgo.ActivityType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "listening":
		return discordgo.ActivityTypeListening
	case "watching":
		return discordgo.ActivityTypeWatching
	case "competing":
		return discordgo.ActivityTypeCompeting
	case "streaming":
		// A streaming activity needs a live Twitch/YouTube URL; without one the
		// gateway rejects the whole presence, so fall back to playing.
		return discordgo.ActivityTypeGame
	}
	return discordgo.ActivityTypeGame
}

func cut(s string) string {
	r := []rune(s)
	if len(r) <= maxPresenceText {
		return s
	}
	return string(r[:maxPresenceText])
}
