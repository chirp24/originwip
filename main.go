package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	g "xabbo.b7c.io/goearth"
	"xabbo.b7c.io/goearth/shockwave/in"
	"xabbo.b7c.io/goearth/shockwave/out"
)

var ext = g.NewExt(g.ExtInfo{
	Title:       "easydice",
	Description: "An extension to roll/reset dice for you.",
	Author:      "chirp24",
	Version:     "1.3",
})

var (
	throwDice      []*g.Packet
	diceOff        []*g.Packet
	setup          bool
	throwChannel   chan *g.Packet
	diceOffChannel chan *g.Packet
	setupMutex     sync.Mutex
	diceValues     map[int]int
	diceIDs        []int
)

func main() {
	ext.Initialized(onInitialized)
	ext.Connected(onConnected)
	ext.Disconnected(onDisconnected)
	ext.Intercept(out.CHAT, out.SHOUT, out.WHISPER).With(handleChat)
	ext.Intercept(out.THROW_DICE).With(handleThrowDice)
	ext.Intercept(out.DICE_OFF).With(handleDiceOff)
	ext.Intercept(in.DICE_VALUE).With(handleDiceResults)

	throwChannel = make(chan *g.Packet, 5)
	diceOffChannel = make(chan *g.Packet, 5)
	diceValues = make(map[int]int)
	diceIDs = make([]int, 5)

	go handleThrowDiceSetup()
	go handleDiceOffSetup()

	ext.Run()
}

func onInitialized(e g.InitArgs) {
	log.Println("Extension initialized")
}

func onConnected(e g.ConnectArgs) {
	log.Printf("Game connected (%s)\n", e.Host)
}

func onDisconnected() {
	log.Println("Game disconnected")
}

func handleChat(e *g.Intercept) {
	msg := e.Packet.ReadString()
	if strings.Contains(msg, ":close") {
		e.Block()
		log.Println(msg)
		setupMutex.Lock()
		setup = false
		setupMutex.Unlock()
		go closeDice()
	} else if strings.Contains(msg, ":setup") {
		e.Block()
		log.Println(msg)
		setupMutex.Lock()
		setup = true
		setupMutex.Unlock()
		resetPackets() // Reset packets on setup
	} else if strings.Contains(msg, ":roll") {
		e.Block()
		log.Println(msg)
		go rollDice()
	}
}

func resetPackets() {
	setupMutex.Lock()
	defer setupMutex.Unlock()
	throwDice = []*g.Packet{nil, nil, nil, nil, nil}
	diceOff = []*g.Packet{nil, nil, nil, nil, nil}
	diceValues = make(map[int]int)
	diceIDs = make([]int, 5)
	log.Println("All saved packets reset")
}

func handleThrowDice(e *g.Intercept) {
	setupMutex.Lock()
	defer setupMutex.Unlock()
	if setup {
		throwChannel <- e.Packet.Copy()
	}
}

func handleDiceOff(e *g.Intercept) {
	setupMutex.Lock()
	defer setupMutex.Unlock()
	if setup {
		diceOffChannel <- e.Packet.Copy()
	}
}

func handleThrowDiceSetup() {
	for packet := range throwChannel {
		setupMutex.Lock()
		packetString := packet.ReadString()
		if strings.HasPrefix(packetString, "AZ") && len(packetString) > 2 {
			diceIDStr := packetString[2:] // Extract the ID part after "AZ"
			diceID, err := strconv.Atoi(diceIDStr)
			if err != nil {
				log.Printf("Error extracting dice ID: %v\n", err)
				setupMutex.Unlock()
				continue
			}
			for i := 0; i < 5; i++ {
				if throwDice[i] == nil {
					throwDice[i] = packet
					diceIDs[i] = diceID
					log.Printf("Throw Dice %d set: %v, Dice ID: %d\n", i+1, throwDice[i], diceIDs[i])
					break
				}
			}
		} else {
			log.Printf("Unexpected packet format: %s\n", packetString)
		}
		setupMutex.Unlock()
	}
	log.Println("Throw Dice Setup complete")
}

func handleDiceOffSetup() {
	for packet := range diceOffChannel {
		setupMutex.Lock()
		for i := 0; i < 5; i++ {
			if diceOff[i] == nil {
				diceOff[i] = packet
				log.Printf("Dice Off %d set: %v\n", i+1, diceOff[i])
				break
			}
		}
		setupMutex.Unlock()
	}
	log.Println("Dice Off Setup complete")
}

func closeDice() {
	setupMutex.Lock()
	defer setupMutex.Unlock()
	for i := 0; i < 5; i++ {
		if diceOff[i] != nil {
			ext.SendPacket(diceOff[i])
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func rollDice() {
	setupMutex.Lock()
	defer setupMutex.Unlock()
	for i := 0; i < 5; i++ {
		if throwDice[i] != nil {
			ext.SendPacket(throwDice[i])
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func handleDiceResults(e *g.Intercept) {
	diceID, obfuscatedValue, err := parseDiceValuePacket(e.Packet)
	if err != nil {
		log.Printf("Error parsing dice value packet: %v\n", err)
		return
	}

	diceValue := obfuscatedValue - diceID*38

	for _, id := range diceIDs {
		if diceID == id {
			diceValues[diceID] = diceValue
			break
		}
	}

	if len(diceValues) == 5 {
		message := evaluateDiceValues(diceValues)
		ext.Send(in.CHAT, 0, message, 0, 34, 0, 0)
		diceValues = make(map[int]int) // Reset for next roll
	}
}

func parseDiceValuePacket(packet *g.Packet) (int, int, error) {
	diceStr := packet.ReadString()
	parts := strings.Split(diceStr, " ")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("packet format incorrect")
	}
	diceID, err := strconv.Atoi(parts[0][2:])
	if err != nil {
		return 0, 0, fmt.Errorf("error parsing dice ID: %v", err)
	}
	obfuscatedValue, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("error parsing obfuscated value: %v", err)
	}
	return diceID, obfuscatedValue, nil
}

func evaluateDiceValues(diceValues map[int]int) string {
	counts := make(map[int]int)
	for _, value := range diceValues {
		counts[value]++
	}

	switch {
	case len(counts) == 1:
		return "Five of a kind!"
	case len(counts) == 2:
		for _, count := range counts {
			if count == 4 {
				return "Four of a kind!"
			}
		}
		return "Full house!"
	case len(counts) == 3:
		for _, count := range counts {
			if count == 3 {
				return "Three of a kind!"
			}
		}
		return "Two pairs!"
	case len(counts) == 4:
		return "One pair!"
	default:
		return "No combination!"
	}
}
