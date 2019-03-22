package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

type pagerdutyUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type slackUser struct {
	RealName string `json:"real_name"`
	Name     string `json:"name"`
	ID       string `json:"id"`
}

type slackUserInfo struct {
	User slackUser `json:"user"`
}

type slackGroup struct {
	Group struct {
		Members []string `json:"members"`
	} `json:"group"`
}

type schedule struct {
	Oncalls []oncall `json:oncalls`
}

type oncall struct {
	User pagerdutyUser `json:"user"`
}

const (
	today      = "Current"
	nextDay    = "Next"
	timeFormat = "Mon, Jan 02"
)

func main() {
	key := os.Getenv("PAGERDUTY_API_KEY")
	if key == "" {
		log.Fatal(fmt.Errorf("Empty or unset environment variable PAGERDUTY_API_KEY"))
		os.Exit(1)
	}

	escalationPolicy := os.Getenv("PAGERDUTY_ESCALATION_POLICY")
	if escalationPolicy == "" {
		log.Fatal(fmt.Errorf("Empty or unset environment variable PAGERDUTY_ESCALATION_POLICY"))
		os.Exit(1)
	}

	slackToken := os.Getenv("SLACK_TOKEN")
	if slackToken == "" {
		log.Fatal(fmt.Errorf("Empty or unset environment variable SLACK_TOKEN"))
		os.Exit(1)
	}

	channel := os.Getenv("SLACK_CHANNEL")
	if channel == "" {
		log.Fatal(fmt.Errorf("Empty or unset environment variable SLACK_CHANNEL"))
		os.Exit(1)
	}

	timeframe := os.Getenv("CREW_TIMEFRAME")
	if timeframe == "" {
		log.Fatal(fmt.Errorf("Empty or unset environment variable CREW_TIMEFRAME"))
		os.Exit(1)
	}

	var scheduleDate time.Time
	now := time.Now()

	switch timeframe {
	case today:
		scheduleDate = now
	case nextDay:
		scheduleDate = getNextWorkDay(now)
	default:
		log.Fatal(fmt.Errorf("CREW_TIMEFRAME must be one of: (%s|%s)", today, nextDay))
		os.Exit(1)
	}

	slackUsers := getSlackUsers(slackToken, channel)
	onCallUsers := getPagerdutyUsers(key, escalationPolicy, scheduleDate.Format(time.RFC3339))

	fmt.Print(message(timeframe, scheduleDate.Format(timeFormat), onCallUsers, slackUsers))
}

func getSlackUsers(token, channel string) (users map[string]slackUser) {
	users = make(map[string]slackUser)
	resp, err := http.Get(fmt.Sprintf("https://slack.com/api/groups.info?token=%s&channel=%s", token, channel))
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)

	var group slackGroup
	err = json.Unmarshal(body, &group)

	var userInfo slackUserInfo
	for _, member := range group.Group.Members {
		r, err := http.Get(fmt.Sprintf("https://slack.com/api/users.info?token=%s&user=%s", token, member))
		if err != nil {
			log.Fatal(err)
		}

		body, err := ioutil.ReadAll(r.Body)

		err = json.Unmarshal(body, &userInfo)
		users[userInfo.User.RealName] = userInfo.User
		r.Body.Close()
	}
	return
}

func getPagerdutyUsers(key, escalationPolicy, date string) []pagerdutyUser {
	r, err := onCallSchedule(key, escalationPolicy, date)
	if err != nil {
		log.Fatal(err)
	}

	var users []pagerdutyUser
	seen := make(map[string]struct{}, len(r.Oncalls))
	for _, onCall := range r.Oncalls {
		if _, ok := seen[onCall.User.Email]; ok {
			continue
		}

		seen[onCall.User.Email] = struct{}{}
		users = append(users, onCall.User)
	}

	return users
}

func onCallSchedule(key, escalationPolicy, date string) (resp schedule, err error) {
	u, err := url.Parse(fmt.Sprintf("https://api.pagerduty.com/oncalls?time_zone=UTC&include[]=users&escalation_policy_ids[]=%s&since=%s&until=%s", escalationPolicy, date, date))
	if err != nil {
		return
	}
	req := &http.Request{
		Method: "GET",
		Header: map[string][]string{
			"Accept":        {"application/vnd.pagerduty+json;version=2"},
			"Authorization": {fmt.Sprintf("Token token=%s", key)},
		},
		URL: u,
	}

	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer r.Body.Close()

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return
	}

	if r.StatusCode != http.StatusOK {
		err = fmt.Errorf("%s: %s", r.Status, string(bodyBytes))
		return
	}

	err = json.Unmarshal(bodyBytes, &resp)

	return
}

func message(timeframe, date string, users []pagerdutyUser, slackUsers map[string]slackUser) string {
	msg := timeframe + ` on-call users for ` + date + `:`
	for _, u := range users {
		contactMethod := u.Email

		if _, ok := slackUsers[u.Name]; ok {
			contactMethod = fmt.Sprintf("<@%s>", slackUsers[u.Name].ID)
		}

		msg = fmt.Sprintf(`%s
- %s ( %s )`, msg, u.Name, contactMethod)
	}
	return msg
}

func getNextWorkDay(now time.Time) time.Time {
	var delta = 1
	if now.Weekday() == time.Friday {
		delta = 3
	}
	return now.AddDate(0, 0, delta)
}
