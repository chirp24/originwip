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
	setup          bool
	setupMutex     sync.Mutex
	diceIDs        []int
	diceValues     map[int]int
	throwChannel   chan *g.Packet
	diceOffChannel chan *g.Packet
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
		setupMutex.Unlock()
		go closeDice()
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
		go rollDice()
	}
}

func resetPackets() {
	setupMutex.Lock()
	defer setupMutex.Unlock()
	diceIDs = nil
	diceValues = make(map[int]int)
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
		if len(diceIDs) < 5 {
			packetString := string(packet.Data)
			diceID, err := strconv.Atoi(packetString)
			if err != nil {
				log.Printf("Error parsing dice ID: %v\n", err)
			} else {
				diceIDs = append(diceIDs, diceID)
				log.Printf("Dice ID captured: %d\n", diceID)
			}
		}
		setupMutex.Unlock()
	}
	log.Println("Throw Dice Setup complete")
}

func handleDiceOffSetup() {
	for packet := range diceOffChannel {
		// Process dice off packets if needed
		setupMutex.Lock()
		packetString := string(packet.Data)
		diceID, err := strconv.Atoi(packetString)
		if err != nil {
			log.Printf("Error parsing dice ID: %v\n", err)
		} else {
			diceIDs = append(diceIDs, diceID)
			log.Printf("Dice ID captured: %d\n", diceID)
		}
		setupMutex.Unlock()
	}
	log.Println("Dice Off Setup complete")
}

func closeDice() {

	done := make(chan struct{})

	for _, id := range diceIDs {
		go func(diceID int) {
			packet := fmt.Sprintf("%d", diceID)    // Construct dice off packet
			ext.Send(out.DICE_OFF, []byte(packet)) // Send the packet
			log.Printf("Sent dice close packet for ID: %d\n", diceID)
			done <- struct{}{}
		}(id)

		time.Sleep(500 * time.Millisecond)
	}

	for range diceIDs {
		<-done
	}

	log.Println("All dice close packets sent")
}

func rollDice() {

	done := make(chan struct{})

	for _, id := range diceIDs {
		go func(diceID int) {
			packet := fmt.Sprintf("%d", diceID)      // Construct dice roll packet
			ext.Send(out.THROW_DICE, []byte(packet)) // Send the packet
			log.Printf("Sent dice roll packet for ID: %d\n", diceID)
			done <- struct{}{}
		}(id)

		time.Sleep(500 * time.Millisecond)
	}

	for range diceIDs {
		<-done
	}

	log.Println("All dice roll packets sent")
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

func handleDiceResults(e *g.Intercept) {
	diceID, obfuscatedValue, err := parseDiceValuePacket(e.Packet)
	if err != nil {
		log.Printf("Error parsing dice value packet: %v\n", err)
		return
	}

	setupMutex.Lock()
	defer setupMutex.Unlock()

	// Check if this is the first DICE_VALUE packet with only dice ID
	if obfuscatedValue == 0 {
		for i, id := range diceIDs {
			if diceID == id {
				diceValues[i] = 0 // Set to 0 or some default value
				return
			}
		}
	} else {
		// This is the second DICE_VALUE packet with both dice ID and obfuscated value
		for i, id := range diceIDs {
			if diceID == id {
				diceValues[i] = obfuscatedValue - id*38
				break
			}
		}

		// Check if we have all dice values now
		if len(diceValues) == 5 {
			message := evaluateDiceValues(diceValues)
			ext.Send(in.CHAT, message)     // Sending the result as a chat message
			diceValues = make(map[int]int) // Reset for next roll
		}
	}
}

func evaluateDiceValues(diceValues map[int]int) string {
	// Add logic here to evaluate dice values based on poker rules
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
