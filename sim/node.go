package sim

import (
	"log"
	"time"

	"github.com/simpleiot/simpleiot/api"
	"github.com/simpleiot/simpleiot/data"
)

func packetDelay() {
	time.Sleep(5 * time.Second)
}

// NodeSim simulates a simple node
func NodeSim(portal, nodeID string) {
	log.Printf("starting simulator: ID: %v, portal: %v\n", nodeID, portal)

	sendPoints := api.NewSendPoints(portal, nodeID, "", time.Second*10, false)
	tempSim := NewSim(72, 0.2, 70, 75)
	voltSim := NewSim(2, 0.1, 1, 5)
	voltSim2 := NewSim(5, 0.5, 1, 10)

	for {
		points := make([]data.Point, 3)
		points[0] = data.Point{
			Type:  "temp",
			Value: tempSim.Sim(),
		}

		points[1] = data.Point{
			Key:   "V0",
			Type:  "volt",
			Value: voltSim.Sim(),
		}

		points[2] = data.Point{
			Key:   "V1",
			Type:  "volt",
			Value: voltSim2.Sim(),
		}

		err := sendPoints(points)
		if err != nil {
			log.Println("Error sending points: ", err)
		}
		packetDelay()
	}
}
