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
	Version:     "1.9",
})

var (
	setup            bool
	setupMutex       sync.Mutex
	throwChannel     chan *g.Packet
	diceOffChannel   chan *g.Packet
	diceThrowPackets []*g.Packet // Store THROW_DICE packets during setup
	diceOffPackets   []*g.Packet // Store DICE_OFF packets during setup
	diceValues       map[int]int // Store dice values after rolling
	closingDice      bool        // Flag to indicate dice are being closed
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
	if strings.Contains(msg, ":close") { // :close msg
		e.Block()
		log.Println(msg)
		setupMutex.Lock()
		setup = false
		closingDice = true // Set closingDice flag
		setupMutex.Unlock()
		go closeDice() // Run closeDice asynchronously
	} else if strings.Contains(msg, ":setup") { // :setup msg
		e.Block()
		log.Println(msg)
		setupMutex.Lock()
		setup = true
		setupMutex.Unlock()
		// Reset all saved packets
		resetPackets()
	} else if strings.Contains(msg, ":roll") { // :roll msg
		e.Block()
		log.Println(msg)
		go rollDice() // Run rollDice asynchronously
	}
}

func resetPackets() {
	setupMutex.Lock()
	defer setupMutex.Unlock()
	diceThrowPackets = nil         // Reset THROW_DICE packets
	diceOffPackets = nil           // Reset DICE_OFF packets
	diceValues = make(map[int]int) // Reset dice values
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
		if setup {
			diceThrowPackets = append(diceThrowPackets, packet.Copy()) // Store the entire packet
			log.Printf("Stored THROW_DICE packet: %s\n", packet.Data)
		}
		setupMutex.Unlock()
	}
	log.Println("Throw Dice Setup complete")
}

func handleDiceOffSetup() {
	for packet := range diceOffChannel {
		setupMutex.Lock()
		if setup {
			diceOffPackets = append(diceOffPackets, packet.Copy()) // Store the entire packet
			log.Printf("Stored DICE_OFF packet: %s\n", packet.Data)
		}
		setupMutex.Unlock()
	}
	log.Println("Dice Off Setup complete")
}

func closeDice() {
	setupMutex.Lock()
	defer setupMutex.Unlock()

	// Send each stored DICE_OFF packet in sequence with a delay
	for _, packet := range diceOffPackets {
		log.Printf("Sending DICE_OFF packet: %s\n", packet.Data)
		ext.SendPacket(packet)
		time.Sleep(500 * time.Millisecond) // Add delay
	}

	log.Println("Closing dice")
}

func rollDice() {
	setupMutex.Lock()
	defer setupMutex.Unlock()

	// Send each stored THROW_DICE packet in sequence with a delay
	for _, packet := range diceThrowPackets {
		log.Printf("Sending THROW_DICE packet: %s\n", packet.Data)
		ext.SendPacket(packet)
		time.Sleep(500 * time.Millisecond) // Add delay
	}

	log.Println("Rolling dice")
}

func handleDiceResults(e *g.Intercept) {
	setupMutex.Lock()
	defer setupMutex.Unlock()

	if setup && !closingDice {
		return // Ignore if still in setup mode and not closing dice
	}

	diceID, obfuscatedValue, err := parseDiceValuePacket(e.Packet)
	if err != nil {
		log.Printf("Error parsing dice value packet: %v\n", err)
		return
	}

	// Store the dice value only if it's a valid roll and not closing dice
	if obfuscatedValue != 0 && !closingDice {
		diceValues[diceID] = obfuscatedValue - diceID*38
	}

	// Check if all dice have been rolled and evaluate poker hands if needed
	if len(diceValues) == len(diceThrowPackets) && !closingDice {
		message := evaluateDiceValues(diceValues)
		ext.Send(in.CHAT, 0, message, 0, 34, 0, 0)

		// Reset for next setup or roll
		resetPackets()
	}
}

func parseDiceValuePacket(packet *g.Packet) (int, int, error) {
	packetString := string(packet.Data) // Convert packet data to string
	parts := strings.Fields(packetString)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("packet format incorrect")
	}

	diceID, err := strconv.Atoi(parts[0])
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
	// Implement logic here to evaluate dice values based on poker rules
	// Example logic:
	var counts = make(map[int]int)
	for _, value := range diceValues {
		counts[value]++
	}

	// Check for poker hands based on counts
	switch len(counts) {
	case 5:
		return "High card!"
	case 4:
		return "One pair!"
	case 3:
		for _, count := range counts {
			if count == 3 {
				return "Three of a kind!"
			}
		}
		return "Two pair!"
	case 2:
		for _, count := range counts {
			if count == 4 {
				return "Four of a kind!"
			}
		}
		return "Full house!"
	default:
		return "Invalid hand!"
	}
}
