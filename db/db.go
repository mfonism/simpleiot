package db

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/simpleiot/simpleiot/data"
	"github.com/timshannon/bolthold"
	bolt "go.etcd.io/bbolt"
)

// This file contains database manipulations.

// Db is used for all db access in the application.
// We will eventually turn this into an interface to
// handle multiple Db backends.
type Db struct {
	store  *bolthold.Store
	influx *Influx
}

// NewDb creates a new Db instance for the app
func NewDb(dataDir string, influx *Influx, init bool) (*Db, error) {
	dbFile := path.Join(dataDir, "data.db")
	store, err := bolthold.Open(dbFile, 0666, nil)
	if err != nil {
		log.Println("bolthold open failed: ", err)
		return nil, err
	}

	db := &Db{store: store, influx: influx}
	if init {
		return db, db.initialize()
	}

	return db, nil
}

func (db *Db) update(fn func(tx *bolt.Tx) error) error {
	return db.store.Bolt().Update(fn)
}

func (db *Db) view(fn func(tx *bolt.Tx) error) error {
	return db.store.Bolt().View(fn)
}

// Node returns data for a particular device
func (db *Db) Node(id string) (ret data.Node, err error) {
	err = db.store.Get(id, &ret)
	return
}

// Nodes returns all devices.
func (db *Db) Nodes() (ret []data.Node, err error) {
	err = db.store.Find(&ret, nil)
	return
}

// NodeByID returns a device for a given ID
func (db *Db) NodeByID(id string) (data.Node, error) {
	var ret data.Node
	if err := db.store.Get(id, &ret); err != nil {
		return ret, err
	}

	return ret, nil
}

// NodeEach iterates through each device calling provided function
func (db *Db) NodeEach(callback func(device *data.Node) error) error {
	return db.store.ForEach(nil, callback)
}

// NodeDelete deletes a device from the database
func (db *Db) NodeDelete(id string) error {
	return db.update(func(tx *bolt.Tx) error {
		// first delete all rules for device
		var device data.Node
		err := db.store.TxGet(tx, id, &device)
		if err != nil {
			return err
		}

		for _, r := range device.Rules {
			err := db.store.TxDelete(tx, r.ID, data.Rule{})
			if err != nil {
				return err
			}
		}
		return db.store.TxDelete(tx, id, data.Node{})
	})
}

// NodeUpdateGroups updates the groups for a device.
func (db *Db) NodeUpdateGroups(id string, groups []uuid.UUID) error {
	return db.update(func(tx *bolt.Tx) error {
		var dev data.Node
		if err := db.store.TxGet(tx, id, &dev); err != nil {
			return err
		}

		dev.Groups = groups

		return db.store.TxUpdate(tx, id, dev)
	})
}

var zero uuid.UUID

// NodePoint processes a Point for a particular device
func (db *Db) NodePoint(id string, point data.Point) error {
	// for now, we process one point at a time. We may eventually
	// want to create NodeSamples to process multiple samples so
	// we can batch influx writes for performance

	if db.influx != nil {
		points := []InfluxSample{
			PointToInfluxSample(id, point),
		}
		err := db.influx.WriteSamples(points)
		if err != nil {
			log.Println("Error writing particle samples to influx: ", err)
		}
	}

	return db.update(func(tx *bolt.Tx) error {
		var dev data.Node
		err := db.store.TxGet(tx, id, &dev)
		if err != nil {
			if err == bolthold.ErrNotFound {
				dev.ID = id
			} else {
				return err
			}
		}

		dev.ProcessPoint(point)
		dev.SetState(data.SysStateOnline)
		return db.store.TxUpsert(tx, id, dev)
	})
}

// NodeSetState is used to set the current system state
func (db *Db) NodeSetState(id string, state int) error {
	return db.update(func(tx *bolt.Tx) error {
		var dev data.Node
		err := db.store.TxGet(tx, id, &dev)
		if err != nil {
			if err == bolthold.ErrNotFound {
				dev.ID = id
			} else {
				return err
			}
		}

		dev.SetState(state)
		return db.store.TxUpsert(tx, id, dev)
	})
}

// NodeSetSwUpdateState is used to set the SW update state of the device
func (db *Db) NodeSetSwUpdateState(id string, state data.SwUpdateState) error {
	return db.update(func(tx *bolt.Tx) error {
		var dev data.Node
		err := db.store.TxGet(tx, id, &dev)
		if err != nil {
			if err == bolthold.ErrNotFound {
				dev.ID = id
			} else {
				return err
			}
		}

		dev.SetSwUpdateState(state)
		return db.store.TxUpsert(tx, id, dev)
	})
}

// NodeSetCmd sets a cmd for a device, and sets the
// CmdPending flag in the device structure.
func (db *Db) NodeSetCmd(cmd data.NodeCmd) error {
	return db.update(func(tx *bolt.Tx) error {
		err := db.store.TxUpsert(tx, cmd.ID, &cmd)
		if err != nil {
			return err
		}

		// and set the device pending flag
		var dev data.Node
		err = db.store.TxGet(tx, cmd.ID, &dev)
		if err != nil {
			return err
		}

		dev.SetCmdPending(true)
		return db.store.TxUpdate(tx, cmd.ID, dev)
	})
}

// NodeDeleteCmd delets a cmd for a device and clears the
// the cmd pending flag
func (db *Db) NodeDeleteCmd(id string) error {
	return db.update(func(tx *bolt.Tx) error {
		err := db.store.TxDelete(tx, id, data.NodeCmd{})
		if err != nil {
			return err
		}

		// and clear the device pending flag
		var dev data.Node
		err = db.store.TxGet(tx, id, &dev)
		if err != nil {
			return err
		}

		dev.SetCmdPending(false)
		err = db.store.TxUpdate(tx, id, dev)
		if err != nil {
			return err
		}

		return nil
	})
}

// NodeGetCmd gets a cmd for a device. If the cmd is no null,
// the command is deleted, and the cmdPending flag cleared in
// the Node data structure.
func (db *Db) NodeGetCmd(id string) (data.NodeCmd, error) {
	var cmd data.NodeCmd

	err := db.update(func(tx *bolt.Tx) error {
		err := db.store.TxGet(tx, id, &cmd)
		if err == bolthold.ErrNotFound {
			// we don't consider this an error in this case
			err = nil
		}

		if err != nil {
			return err
		}

		if cmd.Cmd != "" {
			// a device has fetched a command, delete it
			err := db.store.TxDelete(tx, id, data.NodeCmd{})
			if err != nil {
				return err
			}

			// and clear the device pending flag
			var dev data.Node
			err = db.store.TxGet(tx, id, &dev)
			if err != nil {
				return err
			}

			dev.SetCmdPending(false)
			err = db.store.TxUpdate(tx, id, dev)
			if err != nil {
				return err
			}
		}

		return nil
	})

	return cmd, err
}

// NodeCmds returns all commands for device
func (db *Db) NodeCmds() (ret []data.NodeCmd, err error) {
	err = db.store.Find(&ret, nil)
	return
}

// NodesForUser returns all devices for a particular user
func (db *Db) NodesForUser(userID uuid.UUID) ([]data.Node, error) {
	var devices []data.Node

	isRoot, err := db.UserIsRoot(userID)
	if err != nil {
		return devices, err
	}

	if isRoot {
		// return all devices for root users
		err := db.store.Find(&devices, nil)
		return devices, err
	}

	err = db.view(func(tx *bolt.Tx) error {
		// First find groups users is part of
		var allGroups []data.Group
		err := db.store.TxFind(tx, &allGroups, nil)

		if err != nil {
			return err
		}

		var groupIDs []uuid.UUID

		for _, o := range allGroups {
			for _, ur := range o.Users {
				if ur.UserID == userID {
					groupIDs = append(groupIDs, o.ID)
				}
			}
		}

		// next, find devices that are part of the groups
		err = db.store.TxFind(tx, &devices,
			bolthold.Where("Groups").ContainsAny(bolthold.Slice(groupIDs)...))

		return nil
	})

	return devices, err
}

type users []data.User

func (u users) Len() int {
	return len(u)
}

func (u users) Less(i, j int) bool {
	return strings.ToLower((u)[i].FirstName) < strings.ToLower((u)[j].FirstName)
}

func (u users) Swap(i, j int) {
	u[i], u[j] = u[j], u[i]
}

// Users returns all users, sorted by first name.
func (db *Db) Users() ([]data.User, error) {
	var ret users
	err := db.store.Find(&ret, nil)
	// sort users by first name
	sort.Sort(ret)
	return ret, err
}

type privilege string

// UserCheck checks user authenticatino
func (db *Db) UserCheck(email, password string) (*data.User, error) {
	var u data.User
	query := bolthold.Where("Email").Eq(email).
		And("Pass").Eq(password)
	err := db.store.FindOne(&u, query)
	if err != nil {
		if err != bolthold.ErrNotFound {
			return nil, err
		}
		return nil, nil
	}

	return &u, nil
}

// UserIsRoot checks if root user
func (db *Db) UserIsRoot(id uuid.UUID) (bool, error) {
	var group data.Group

	err := db.store.FindOne(&group, bolthold.Where("ID").Eq(zero))

	if err != nil {
		return false, err
	}

	for _, ur := range group.Users {
		if ur.UserID == id {
			return true, nil
		}
	}

	return false, nil

}

// UserByID returns the user with the given ID, if it exists.
func (db *Db) UserByID(id string) (data.User, error) {
	var ret data.User
	if err := db.store.FindOne(&ret, bolthold.Where("ID").Eq(id)); err != nil {
		return ret, err
	}

	return ret, nil
}

// UserByEmail returns the user with the given email, if it exists.
func (db *Db) UserByEmail(email string) (data.User, error) {
	var ret data.User
	if err := db.store.FindOne(&ret, bolthold.Where("Email").Eq(email)); err != nil {
		return ret, err
	}

	return ret, nil
}

// UsersForGroup returns all users who who are connected to a device by a group.
func (db *Db) UsersForGroup(id uuid.UUID) ([]data.User, error) {
	var ret []data.User

	err := db.view(func(tx *bolt.Tx) error {
		var group data.Group
		err := db.store.TxGet(tx, id, &group)
		if err != nil {
			return err
		}

		for _, role := range group.Users {
			var user data.User
			err := db.store.TxGet(tx, role.UserID, &user)
			if err != nil {
				return err
			}
			ret = append(ret, user)
		}
		return nil
	})

	return ret, err
}

// initialize initializes the database with one user (admin)
// in one groupanization (root).
// All devices are properties of the root groupanization.
func (db *Db) initialize() error {
	// initialize root group in new db
	var group data.Group
	err := db.store.FindOne(&group, bolthold.Where("Name").Eq("root"))

	// group was found or we ran into an error
	if err != bolthold.ErrNotFound {
		return err
	}

	// add root group and admin user
	return db.update(func(tx *bolt.Tx) error {
		log.Println("adding root group and admin user ...")

		admin := data.User{
			ID:        zero,
			FirstName: "admin",
			LastName:  "user",
			Email:     "admin@admin.com",
			Pass:      "admin",
		}

		if err := db.store.TxInsert(tx, admin.ID, admin); err != nil {
			return err
		}

		log.Println("Created admin user: ", admin)

		group := data.Group{
			ID:   zero,
			Name: "root",
			Users: []data.UserRoles{
				{UserID: zero, Roles: []data.Role{data.RoleAdmin}},
			},
		}

		if err := db.store.TxInsert(tx, group.ID, group); err != nil {
			return err
		}

		log.Println("Created root group:", group)
		return nil
	})
}

// NodesForGroup returns the devices which are property of the given Group.
func (db *Db) NodesForGroup(tx *bolt.Tx, groupID uuid.UUID) ([]data.Node, error) {
	var devices []data.Node
	err := db.store.TxFind(tx, &devices, bolthold.Where("Groups").Contains(groupID))
	return devices, err
}

// UserInsert inserts a new user
func (db *Db) UserInsert(user data.User) (string, error) {
	id := uuid.New()
	err := db.store.Insert(id, user)
	return id.String(), err
}

// UserUpdate updates a new user
func (db *Db) UserUpdate(user data.User) error {
	return db.update(func(tx *bolt.Tx) error {
		if err := db.store.TxUpdate(tx, user.ID, user); err != nil {
			log.Printf("Error updating user %v, try fixing key\n", user.Email)

			// Delete current user with bad key
			err := db.store.TxDeleteMatching(tx, data.User{},
				bolthold.Where("ID").Eq(user.ID))

			if err != nil {
				log.Println("Error deleting user when trying to fix up: ", err)
				return err
			}

			// try to insert group
			if err = db.store.TxUpsert(tx, user.ID, user); err != nil {
				log.Println("Error updating user after delete: ", err)
				return err
			}

			return err
		}

		return nil
	})
}

// UserDelete deletes a user from the database
func (db *Db) UserDelete(id uuid.UUID) error {
	return db.store.Delete(id, data.User{})
}

// Groups returns all groups.
func (db *Db) Groups() ([]data.Group, error) {
	var ret []data.Group
	if err := db.store.Find(&ret, nil); err != nil {
		return ret, fmt.Errorf("problem finding groups: %v", err)
	}

	return ret, nil
}

// Group returns the Group with the given ID.
func (db *Db) Group(id uuid.UUID) (data.Group, error) {
	var group data.Group
	err := db.store.FindOne(&group, bolthold.Where("ID").Eq(id))
	return group, err
}

// GroupInsert inserts a new group
func (db *Db) GroupInsert(group data.Group) (string, error) {
	id := uuid.New()

	group.Parent = zero
	err := db.store.Insert(id, group)
	return id.String(), err
}

// GroupUpdate updates a group
func (db *Db) GroupUpdate(gUpdate data.Group) error {
	return db.update(func(tx *bolt.Tx) error {
		if err := db.store.TxUpdate(tx, gUpdate.ID, gUpdate); err != nil {
			log.Printf("Error updating group %v, try fixing key\n", gUpdate.Name)

			// Delete current group with bad key
			err := db.store.TxDeleteMatching(tx, data.Group{},
				bolthold.Where("ID").Eq(gUpdate.ID))

			if err != nil {
				log.Println("Error deleting group when trying to fix up: ", err)
				return err
			}

			// try to insert group
			if err = db.store.TxUpsert(tx, gUpdate.ID, gUpdate); err != nil {
				log.Println("Error updating group after delete: ", err)
				return err
			}
		}

		return nil
	})
}

// GroupDelete deletes a device from the database
func (db *Db) GroupDelete(id uuid.UUID) error {
	return db.store.Delete(id, data.Group{})
}

// Rules returns all rules.
func (db *Db) Rules() ([]data.Rule, error) {
	var ret []data.Rule
	err := db.store.Find(&ret, nil)
	return ret, err
}

// RuleByID finds a rule given the ID
func (db *Db) RuleByID(id uuid.UUID) (data.Rule, error) {
	var rule data.Rule
	err := db.store.Get(id, &rule)
	return rule, err
}

// RuleInsert inserts a new rule
func (db *Db) RuleInsert(rule data.Rule) (uuid.UUID, error) {
	rule.ID = uuid.New()
	err := db.update(func(tx *bolt.Tx) error {
		err := db.store.TxInsert(tx, rule.ID, rule)
		if err != nil {
			return err
		}

		var device data.Node
		err = db.store.TxGet(tx, rule.Config.NodeID, &device)
		if err != nil {
			return err
		}

		device.Rules = append(device.Rules, rule.ID)

		err = db.store.TxUpdate(tx, device.ID, device)
		return err
	})

	return rule.ID, err
}

// RuleUpdateConfig updates a rule config
func (db *Db) RuleUpdateConfig(id uuid.UUID, config data.RuleConfig) error {
	return db.update(func(tx *bolt.Tx) error {
		var rule data.Rule
		if err := db.store.TxGet(tx, id, &rule); err != nil {
			return err
		}

		rule.Config = config

		return db.store.TxUpdate(tx, id, rule)
	})
}

// RuleUpdateState updates a rule state
func (db *Db) RuleUpdateState(id uuid.UUID, state data.RuleState) error {
	return db.update(func(tx *bolt.Tx) error {
		var rule data.Rule
		if err := db.store.TxGet(tx, id, &rule); err != nil {
			return err
		}

		rule.State = state

		return db.store.TxUpdate(tx, id, rule)
	})
}

// RuleDelete deletes a rule from the database
func (db *Db) RuleDelete(id uuid.UUID) error {
	return db.update(func(tx *bolt.Tx) error {
		var rule data.Rule
		err := db.store.TxGet(tx, id, &rule)
		if err != nil {
			return err
		}
		// delete references from device
		var device data.Node
		err = db.store.TxGet(tx, rule.Config.NodeID, &device)
		if err != nil {
			return err
		}
		return db.store.TxDelete(tx, id, data.Rule{})
	})
}

// RuleEach iterates through each rule calling provided function
func (db *Db) RuleEach(callback func(rule *data.Rule) error) error {
	return db.store.ForEach(nil, callback)
}

func newIfZero(id uuid.UUID) uuid.UUID {
	if id == zero {
		return uuid.New()
	}
	return id
}

type dbDump struct {
	Nodes    []data.Node    `json:"devices"`
	Users    []data.User    `json:"users"`
	Groups   []data.Group   `json:"groups"`
	Rules    []data.Rule    `json:"rules"`
	NodeCmds []data.NodeCmd `json:"deviceCmds"`
}

// DumpDb dumps the entire db to a file
func DumpDb(db *Db, out io.Writer) error {
	dump := dbDump{}

	var err error

	dump.Nodes, err = db.Nodes()
	if err != nil {
		return err
	}

	dump.Users, err = db.Users()
	if err != nil {
		return err
	}

	dump.Groups, err = db.Groups()
	if err != nil {
		return err
	}

	dump.Rules, err = db.Rules()
	if err != nil {
		return err
	}

	dump.NodeCmds, err = db.NodeCmds()
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "   ")

	return encoder.Encode(dump)
}
