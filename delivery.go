package main

// delivery.go: sending alerts without upsetting Discord.
//   - DMs go through a queue and leave one at a time, spaced out, because bursts of DMs
//     get bots flagged as spam.
//   - Requests that fail for good (DMs closed, channel deleted, access lost) aren't
//     repeated forever: Discord temporarily blocks bots that make too many failed
//     requests (about 10,000 in 10 minutes).

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// apiCode is Discord's error code for a failed request, or 0.
func apiCode(err error) int {
	var re *discordgo.RESTError
	if errors.As(err, &re) && re.Message != nil {
		return re.Message.Code
	}
	return 0
}

// channelUnusable reports errors that won't fix themselves by trying again.
func channelUnusable(err error) bool {
	switch apiCode(err) {
	case discordgo.ErrCodeUnknownChannel, discordgo.ErrCodeMissingAccess, discordgo.ErrCodeMissingPermissions:
		return true
	}
	return false
}

// postToChannel posts to a server's tracking channel. If the channel is gone or
// Shiron lost access, posting is paused for that server until an admin picks a
// channel in /servers list, instead of failing again on every map change.
// It marks sv as paused right away, so later posts in the same round are skipped too.
func postToChannel(s *discordgo.Session, sv *Server, users []string, parts ...discordgo.MessageComponent) {
	if sv.Paused {
		return
	}
	err := send(s, sv.ChannelID, users, parts...)
	if err == nil {
		return
	}
	if channelUnusable(err) {
		slog.Warn("can't post in channel: posts paused until an admin picks a channel", "guild", sv.GuildID, "channel", sv.ChannelID, "err", err)
		sv.Paused = true
		if err := db.SetPaused(sv.ID, true); err != nil {
			slog.Error("pause server", "err", err)
		}
		return
	}
	slog.Warn("post to channel", "guild", sv.GuildID, "channel", sv.ChannelID, "err", err)
}

type dmJob struct {
	user  string
	parts []discordgo.MessageComponent
}

const (
	dmGap         = 500 * time.Millisecond // between DMs
	dmQueueSize   = 10000                  // beyond this, new DMs are dropped (and logged)
	dmFailsToStop = 2                      // closed-DM failures before switching to channel mentions
)

var (
	dmQueue = make(chan dmJob, dmQueueSize)

	dmMu       sync.Mutex
	dmChannels = map[string]string{} // user -> DM channel ID, so each DM is one request, not two
	dmFails    = map[string]int{}
)

// forgetDMUser drops what Shiron remembers in memory about a user's DMs.
func forgetDMUser(user string) {
	dmMu.Lock()
	defer dmMu.Unlock()
	delete(dmChannels, user)
	delete(dmFails, user)
}

// queueDM schedules a DM; it never blocks the poller.
func queueDM(user string, parts ...discordgo.MessageComponent) {
	select {
	case dmQueue <- dmJob{user, parts}:
	default:
		slog.Warn("DM queue full, alert dropped", "user", user)
	}
}

// runDMSender sends queued DMs one at a time, dmGap apart, until ctx ends.
func runDMSender(ctx context.Context, s *discordgo.Session) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-dmQueue:
			deliverDM(s, job)
			select {
			case <-ctx.Done():
				return
			case <-time.After(dmGap):
			}
		}
	}
}

// drainDMs sends everything queued right away (tests use it instead of the sender).
func drainDMs(s *discordgo.Session) {
	for {
		select {
		case job := <-dmQueue:
			deliverDM(s, job)
		default:
			return
		}
	}
}

func deliverDM(s *discordgo.Session, job dmJob) {
	defer guard("dm")
	dmMu.Lock()
	chID := dmChannels[job.user]
	dmMu.Unlock()
	if chID == "" {
		ch, err := s.UserChannelCreate(job.user)
		if err != nil {
			slog.Info("open DM failed", "user", job.user, "err", err)
			return
		}
		chID = ch.ID
		dmMu.Lock()
		dmChannels[job.user] = chID
		dmMu.Unlock()
	}
	err := send(s, chID, nil, job.parts...)
	dmMu.Lock()
	defer dmMu.Unlock()
	switch {
	case err == nil:
		delete(dmFails, job.user)
	case apiCode(err) == discordgo.ErrCodeCannotSendMessagesToThisUser:
		// DMs closed (or Shiron blocked). Stop trying: switch them to channel mentions.
		dmFails[job.user]++
		if dmFails[job.user] >= dmFailsToStop {
			delete(dmFails, job.user)
			if err := db.SetDelivery(job.user, "channel"); err != nil {
				slog.Error("switch to channel mentions", "user", job.user, "err", err)
				return
			}
			slog.Info("DMs closed: alerts switched to channel mentions", "user", job.user)
		}
	default:
		slog.Info("DM failed", "user", job.user, "err", err)
	}
}
