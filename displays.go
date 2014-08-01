// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web handlers for displays.

package main

import (
	"fmt"
	"github.com/gorilla/mux"
	"github.com/mitchellh/mapstructure"
	"io"
	"log"
	"net/http"
	"strconv"
	"text/template"
)

var rules = []string{"G3", "G5", "G10", "G11", "G12", "G14", "G15", "G16", "G17", "G18", "G19", "G21", "G22",
	"G23", "G24", "G25", "G26", "G26-1", "G27", "G28", "G29", "G30", "G31", "G32", "G34", "G35", "G36", "G37",
	"G38", "G39", "G40", "G41", "G42"}

// Renders the pit display which shows scrolling rankings.
func PitDisplayHandler(w http.ResponseWriter, r *http.Request) {
	template, err := template.ParseFiles("templates/pit_display.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*EventSettings
	}{eventSettings}
	err = template.Execute(w, data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// Renders the announcer display which shows team info and scores for the current match.
func AnnouncerDisplayHandler(w http.ResponseWriter, r *http.Request) {
	template := template.New("").Funcs(templateHelpers)
	_, err := template.ParseFiles("templates/announcer_display.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	// Assemble info about the current match.
	matchType := mainArena.currentMatch.CapitalizedType()
	red1 := mainArena.AllianceStations["R1"].team
	red2 := mainArena.AllianceStations["R2"].team
	red3 := mainArena.AllianceStations["R3"].team
	blue1 := mainArena.AllianceStations["B1"].team
	blue2 := mainArena.AllianceStations["B2"].team
	blue3 := mainArena.AllianceStations["B3"].team

	// Assemble info about the saved match result.
	var redScoreSummary, blueScoreSummary *ScoreSummary
	var savedMatchType, savedMatchDisplayName string
	if mainArena.savedMatchResult != nil {
		redScoreSummary = mainArena.savedMatchResult.RedScoreSummary()
		blueScoreSummary = mainArena.savedMatchResult.BlueScoreSummary()
		match, err := db.GetMatchById(mainArena.savedMatchResult.MatchId)
		if err != nil {
			handleWebErr(w, err)
			return
		}
		savedMatchType = match.CapitalizedType()
		savedMatchDisplayName = match.DisplayName
	}
	data := struct {
		*EventSettings
		MatchType             string
		MatchDisplayName      string
		Red1                  *Team
		Red2                  *Team
		Red3                  *Team
		Blue1                 *Team
		Blue2                 *Team
		Blue3                 *Team
		SavedMatchResult      *MatchResult
		SavedMatchType        string
		SavedMatchDisplayName string
		RedScoreSummary       *ScoreSummary
		BlueScoreSummary      *ScoreSummary
	}{eventSettings, matchType, mainArena.currentMatch.DisplayName, red1, red2, red3, blue1, blue2, blue3,
		mainArena.savedMatchResult, savedMatchType, savedMatchDisplayName, redScoreSummary, blueScoreSummary}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// The websocket endpoint for the announcer display client to send control commands and receive status updates.
func AnnouncerDisplayWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	websocket, err := NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer websocket.Close()

	matchLoadTeamsListener := mainArena.matchLoadTeamsNotifier.Listen()
	defer close(matchLoadTeamsListener)
	matchTimeListener := mainArena.matchTimeNotifier.Listen()
	defer close(matchTimeListener)
	scorePostedListener := mainArena.scorePostedNotifier.Listen()
	defer close(scorePostedListener)

	// Send the various notifications immediately upon connection.
	err = websocket.Write("matchTiming", mainArena.matchTiming)
	if err != nil {
		log.Printf("Websocket error: %s", err)
		return
	}
	data := MatchTimeMessage{mainArena.MatchState, int(mainArena.lastMatchTimeSec)}
	err = websocket.Write("matchTime", data)
	if err != nil {
		log.Printf("Websocket error: %s", err)
		return
	}

	// Spin off a goroutine to listen for notifications and pass them on through the websocket.
	go func() {
		for {
			var messageType string
			var message interface{}
			select {
			case _, ok := <-matchLoadTeamsListener:
				if !ok {
					return
				}
				messageType = "reload"
				message = nil
			case matchTimeSec, ok := <-matchTimeListener:
				if !ok {
					return
				}
				messageType = "matchTime"
				message = MatchTimeMessage{mainArena.MatchState, matchTimeSec.(int)}
			case _, ok := <-scorePostedListener:
				if !ok {
					return
				}
				messageType = "reload"
				message = nil
			}
			err = websocket.Write(messageType, message)
			if err != nil {
				// The client has probably closed the connection; nothing to do here.
				return
			}
		}
	}()

	// Loop, waiting for commands and responding to them, until the client closes the connection.
	for {
		messageType, _, err := websocket.Read()
		if err != nil {
			if err == io.EOF {
				// Client has closed the connection; nothing to do here.
				return
			}
			log.Printf("Websocket error: %s", err)
			return
		}

		switch messageType {
		default:
			websocket.WriteError(fmt.Sprintf("Invalid message type '%s'.", messageType))
			continue
		}
	}
}

// Renders the scoring interface which enables input of scores in real-time.
func ScoringDisplayHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	alliance := vars["alliance"]
	if alliance != "red" && alliance != "blue" {
		handleWebErr(w, fmt.Errorf("Invalid alliance '%s'.", alliance))
		return
	}

	template, err := template.ParseFiles("templates/scoring_display.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*EventSettings
		Alliance string
	}{eventSettings, alliance}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// The websocket endpoint for the scoring interface client to send control commands and receive status updates.
func ScoringDisplayWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	alliance := vars["alliance"]
	if alliance != "red" && alliance != "blue" {
		handleWebErr(w, fmt.Errorf("Invalid alliance '%s'.", alliance))
		return
	}
	var score **RealtimeScore
	if alliance == "red" {
		score = &mainArena.redRealtimeScore
	} else {
		score = &mainArena.blueRealtimeScore
	}

	websocket, err := NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer websocket.Close()

	matchLoadTeamsListener := mainArena.matchLoadTeamsNotifier.Listen()
	defer close(matchLoadTeamsListener)

	// Send the various notifications immediately upon connection.
	err = websocket.Write("score", *score)
	if err != nil {
		log.Printf("Websocket error: %s", err)
		return
	}

	// Spin off a goroutine to listen for notifications and pass them on through the websocket.
	go func() {
		for {
			var messageType string
			var message interface{}
			select {
			case _, ok := <-matchLoadTeamsListener:
				if !ok {
					return
				}
				messageType = "score"
				message = *score
			}
			err = websocket.Write(messageType, message)
			if err != nil {
				// The client has probably closed the connection; nothing to do here.
				return
			}
		}
	}()

	// Loop, waiting for commands and responding to them, until the client closes the connection.
	for {
		messageType, data, err := websocket.Read()
		if err != nil {
			if err == io.EOF {
				// Client has closed the connection; nothing to do here.
				return
			}
			log.Printf("Websocket error: %s", err)
			return
		}

		switch messageType {
		case "preload":
			if !(*score).AutoCommitted {
				preloadedBallsStr, ok := data.(string)
				if !ok {
					websocket.WriteError(fmt.Sprintf("Failed to parse '%s' message.", messageType))
					continue
				}
				preloadedBalls, err := strconv.Atoi(preloadedBallsStr)
				(*score).AutoPreloadedBalls = preloadedBalls
				if err != nil {
					websocket.WriteError(fmt.Sprintf("Failed to parse '%s' message.", messageType))
					continue
				}
			}
		case "mobility":
			if !(*score).AutoCommitted {
				(*score).undoAutoScores = append((*score).undoAutoScores, (*score).CurrentScore)
				(*score).CurrentScore.AutoMobilityBonuses += 1
			}
		case "scoredHighHot":
			if !(*score).AutoCommitted {
				(*score).undoAutoScores = append((*score).undoAutoScores, (*score).CurrentScore)
				(*score).CurrentScore.AutoHighHot += 1
			}
		case "scoredHigh":
			if !(*score).AutoCommitted {
				(*score).undoAutoScores = append((*score).undoAutoScores, (*score).CurrentScore)
				(*score).CurrentScore.AutoHigh += 1
			} else if !(*score).TeleopCommitted && !(*score).CurrentCycle.ScoredHigh {
				(*score).undoCycles = append((*score).undoCycles, (*score).CurrentCycle)
				(*score).CurrentCycle.ScoredHigh = true
				(*score).CurrentCycle.ScoredLow = false
				(*score).CurrentCycle.DeadBall = false
			}
		case "scoredLowHot":
			if !(*score).AutoCommitted {
				(*score).undoAutoScores = append((*score).undoAutoScores, (*score).CurrentScore)
				(*score).CurrentScore.AutoLowHot += 1
			}
		case "scoredLow":
			if !(*score).AutoCommitted {
				(*score).undoAutoScores = append((*score).undoAutoScores, (*score).CurrentScore)
				(*score).CurrentScore.AutoLow += 1
			} else if !(*score).TeleopCommitted && !(*score).CurrentCycle.ScoredLow {
				(*score).undoCycles = append((*score).undoCycles, (*score).CurrentCycle)
				(*score).CurrentCycle.ScoredHigh = false
				(*score).CurrentCycle.ScoredLow = true
				(*score).CurrentCycle.DeadBall = false
			}
		case "assist":
			if !(*score).TeleopCommitted && (*score).CurrentCycle.Assists < 3 {
				(*score).undoCycles = append((*score).undoCycles, (*score).CurrentCycle)
				(*score).CurrentCycle.Assists += 1
			}
		case "truss":
			if !(*score).TeleopCommitted && !(*score).CurrentCycle.Truss {
				(*score).undoCycles = append((*score).undoCycles, (*score).CurrentCycle)
				(*score).CurrentCycle.Truss = true
			}
		case "catch":
			if !(*score).TeleopCommitted && !(*score).CurrentCycle.Catch && (*score).CurrentCycle.Truss {
				(*score).undoCycles = append((*score).undoCycles, (*score).CurrentCycle)
				(*score).CurrentCycle.Catch = true
			}
		case "deadBall":
			if !(*score).TeleopCommitted && !(*score).CurrentCycle.DeadBall {
				(*score).undoCycles = append((*score).undoCycles, (*score).CurrentCycle)
				(*score).CurrentCycle.ScoredHigh = false
				(*score).CurrentCycle.ScoredLow = false
				(*score).CurrentCycle.DeadBall = true
			}
		case "commit":
			if !(*score).AutoCommitted {
				(*score).AutoCommitted = true
			} else if !(*score).TeleopCommitted {
				if (*score).CurrentCycle.ScoredHigh || (*score).CurrentCycle.ScoredLow ||
					(*score).CurrentCycle.DeadBall {
					// Check whether this is a leftover ball from autonomous.
					if ((*score).AutoPreloadedBalls - (*score).CurrentScore.AutoHighHot -
						(*score).CurrentScore.AutoHigh - (*score).CurrentScore.AutoLowHot -
						(*score).CurrentScore.AutoLow - (*score).CurrentScore.AutoClearHigh -
						(*score).CurrentScore.AutoClearLow - (*score).CurrentScore.AutoClearDead) > 0 {
						if (*score).CurrentCycle.ScoredHigh {
							(*score).CurrentScore.AutoClearHigh += 1
						} else if (*score).CurrentCycle.ScoredLow {
							(*score).CurrentScore.AutoClearLow += 1
						} else {
							(*score).CurrentScore.AutoClearDead += 1
						}
					} else {
						(*score).CurrentScore.Cycles = append((*score).CurrentScore.Cycles, (*score).CurrentCycle)
					}
					(*score).CurrentCycle = Cycle{}
					(*score).undoCycles = []Cycle{}
				}
			}
		case "commitMatch":
			(*score).AutoCommitted = true
			(*score).TeleopCommitted = true
			if (*score).CurrentCycle != (Cycle{}) {
				// Commit last cycle.
				(*score).CurrentScore.Cycles = append((*score).CurrentScore.Cycles, (*score).CurrentCycle)
			}
		case "undo":
			if !(*score).AutoCommitted && len((*score).undoAutoScores) > 0 {
				(*score).CurrentScore = (*score).undoAutoScores[len((*score).undoAutoScores)-1]
				(*score).undoAutoScores = (*score).undoAutoScores[0 : len((*score).undoAutoScores)-1]
			} else if !(*score).TeleopCommitted && len((*score).undoCycles) > 0 {
				(*score).CurrentCycle = (*score).undoCycles[len((*score).undoCycles)-1]
				(*score).undoCycles = (*score).undoCycles[0 : len((*score).undoCycles)-1]
			}
		default:
			websocket.WriteError(fmt.Sprintf("Invalid message type '%s'.", messageType))
			continue
		}

		// Send out the score again after handling the command, as it most likely changed as a result.
		err = websocket.Write("score", *score)
		if err != nil {
			log.Printf("Websocket error: %s", err)
			return
		}
	}
}

// Renders the referee interface for assigning fouls.
func RefereeDisplayHandler(w http.ResponseWriter, r *http.Request) {
	template := template.New("").Funcs(templateHelpers)
	_, err := template.ParseFiles("templates/referee_display.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}

	match := mainArena.currentMatch
	matchType := match.CapitalizedType()
	data := struct {
		*EventSettings
		MatchType        string
		MatchDisplayName string
		Red1             int
		Red2             int
		Red3             int
		Blue1            int
		Blue2            int
		Blue3            int
		RedFouls         []Foul
		BlueFouls        []Foul
		Rules            []string
		EntryEnabled     bool
	}{eventSettings, matchType, match.DisplayName, match.Red1, match.Red2, match.Red3, match.Blue1, match.Blue2,
		match.Blue3, mainArena.redRealtimeScore.Fouls, mainArena.blueRealtimeScore.Fouls, rules,
		!(mainArena.redRealtimeScore.FoulsCommitted && mainArena.blueRealtimeScore.FoulsCommitted)}
	err = template.ExecuteTemplate(w, "referee_display.html", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

// The websocket endpoint for the refereee interface client to send control commands and receive status updates.
func RefereeDisplayWebsocketHandler(w http.ResponseWriter, r *http.Request) {
	websocket, err := NewWebsocket(w, r)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	defer websocket.Close()

	matchLoadTeamsListener := mainArena.matchLoadTeamsNotifier.Listen()
	defer close(matchLoadTeamsListener)

	// Spin off a goroutine to listen for notifications and pass them on through the websocket.
	go func() {
		for {
			var messageType string
			var message interface{}
			select {
			case _, ok := <-matchLoadTeamsListener:
				if !ok {
					return
				}
				messageType = "reload"
				message = nil
			}
			err = websocket.Write(messageType, message)
			if err != nil {
				// The client has probably closed the connection; nothing to do here.
				return
			}
		}
	}()

	// Loop, waiting for commands and responding to them, until the client closes the connection.
	for {
		messageType, data, err := websocket.Read()
		if err != nil {
			if err == io.EOF {
				// Client has closed the connection; nothing to do here.
				return
			}
			log.Printf("Websocket error: %s", err)
			return
		}

		switch messageType {
		case "addFoul":
			args := struct {
				Alliance    string
				TeamId      int
				Rule        string
				IsTechnical bool
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				websocket.WriteError(err.Error())
				continue
			}

			// Add the foul to the correct alliance's list.
			foul := Foul{TeamId: args.TeamId, Rule: args.Rule, IsTechnical: args.IsTechnical,
				TimeInMatchSec: mainArena.MatchTimeSec()}
			if args.Alliance == "red" {
				mainArena.redRealtimeScore.Fouls = append(mainArena.redRealtimeScore.Fouls, foul)
			} else {
				mainArena.blueRealtimeScore.Fouls = append(mainArena.blueRealtimeScore.Fouls, foul)
			}
		case "deleteFoul":
			args := struct {
				Alliance       string
				TeamId         int
				Rule           string
				TimeInMatchSec float64
				IsTechnical    bool
			}{}
			err = mapstructure.Decode(data, &args)
			if err != nil {
				websocket.WriteError(err.Error())
				continue
			}

			// Remove the foul from the correct alliance's list.
			deleteFoul := Foul{TeamId: args.TeamId, Rule: args.Rule, IsTechnical: args.IsTechnical,
				TimeInMatchSec: args.TimeInMatchSec}
			var fouls *[]Foul
			if args.Alliance == "red" {
				fouls = &mainArena.redRealtimeScore.Fouls
			} else {
				fouls = &mainArena.blueRealtimeScore.Fouls
			}
			for i, foul := range *fouls {
				if foul == deleteFoul {
					*fouls = append((*fouls)[:i], (*fouls)[i+1:]...)
					break
				}
			}
		case "commitMatch":
			mainArena.redRealtimeScore.FoulsCommitted = true
			mainArena.blueRealtimeScore.FoulsCommitted = true
		default:
			websocket.WriteError(fmt.Sprintf("Invalid message type '%s'.", messageType))
			continue
		}

		// Force a reload of the client to render the updated foul list.
		err = websocket.Write("reload", nil)
		if err != nil {
			log.Printf("Websocket error: %s", err)
			return
		}
	}
}