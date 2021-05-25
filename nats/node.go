package nats

import (
	"fmt"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/data"
)

// GetNode over NATS
func GetNode(nc *natsgo.Conn, id string) (data.Node, error) {
	nodeMsg, err := nc.Request("node."+id, nil, time.Second*20)
	if err != nil {
		return data.Node{}, err
	}

	node, err := data.PbDecodeNode(nodeMsg.Data)

	if err != nil {
		return data.Node{}, err
	}

	return node, nil
}

// GetNodeChildren over NATS (immediate children only, not recursive)
func GetNodeChildren(nc *natsgo.Conn, id string) ([]data.Node, error) {
	nodeMsg, err := nc.Request("node."+id+".children", nil, time.Second*20)
	if err != nil {
		return nil, err
	}

	nodes, err := data.PbDecodeNodes(nodeMsg.Data)

	if err != nil {
		return nil, err
	}

	return nodes, nil
}

// SendNode is used to recursively send a node and children over nats
func SendNode(src, dest *natsgo.Conn, id, parent string) error {
	node, err := GetNode(src, id)
	if err != nil {
		return fmt.Errorf("Error getting local node: %v", err)
	}

	points := node.Points

	points = append(points, data.Point{
		Type: data.PointTypeNodeType,
		Text: node.Type,
	})

	if parent != "" {
		points = append(points, data.Point{
			Type: data.PointTypeParent,
			Text: parent,
		})
	}

	err = SendPoints(dest, id, points, true)

	if err != nil {
		return fmt.Errorf("Error sending node upstream: %v", err)
	}

	// process child nodes
	childNodes, err := GetNodeChildren(src, id)
	if err != nil {
		return fmt.Errorf("Error getting node children: %v", err)
	}

	for _, childNode := range childNodes {
		err := SendNode(src, dest, childNode.ID, id)

		if err != nil {
			return fmt.Errorf("Error sending child node: %v", err)
		}
	}

	return nil
}
