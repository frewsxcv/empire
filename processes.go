package empire

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/jinzhu/gorm"
	"github.com/lib/pq/hstore"
	. "github.com/remind101/empire/pkg/bytesize"
	"github.com/remind101/empire/pkg/constraints"
	"github.com/remind101/empire/procfile"
)

var (
	Constraints1X = Constraints{constraints.CPUShare(256), constraints.Memory(512 * MB)}
	Constraints2X = Constraints{constraints.CPUShare(512), constraints.Memory(1 * GB)}
	ConstraintsPX = Constraints{constraints.CPUShare(1024), constraints.Memory(6 * GB)}

	// NamedConstraints maps a heroku dynos size to a Constraints.
	NamedConstraints = map[string]Constraints{
		"1X": Constraints1X,
		"2X": Constraints2X,
		"PX": ConstraintsPX,
	}

	// DefaultConstraints defaults to 1X process size.
	DefaultConstraints = Constraints1X
)

// ProcessQuantityMap represents a map of process types to quantities.
type ProcessQuantityMap map[ProcessType]int

// DefaultQuantities maps a process type to the default number of instances to
// run.
var DefaultQuantities = ProcessQuantityMap{
	"web": 1,
}

// ProcessType represents the type of a given process/command.
type ProcessType string

// Scan implements the sql.Scanner interface.
func (p *ProcessType) Scan(src interface{}) error {
	if src, ok := src.([]byte); ok {
		*p = ProcessType(src)
	}

	return nil
}

// Value implements the driver.Value interface.
func (p ProcessType) Value() (driver.Value, error) {
	return driver.Value(string(p)), nil
}

// Command represents the actual shell command that gets executed for a given
// ProcessType.
type Command string

// Scan implements the sql.Scanner interface.
func (c *Command) Scan(src interface{}) error {
	if src, ok := src.([]byte); ok {
		*c = Command(src)
	}

	return nil
}

// Value implements the driver.Value interface.
func (c Command) Value() (driver.Value, error) {
	return driver.Value(string(c)), nil
}

// Process holds configuration information about a Process Type.
type Process struct {
	ReleaseID string
	ID        string
	Type      ProcessType
	Quantity  int
	Command   Command
	Port      int `sql:"-"`
	Constraints
}

// NewProcess returns a new Process instance.
func NewProcess(t ProcessType, cmd Command) *Process {
	return &Process{
		Type:        t,
		Quantity:    DefaultQuantities[t],
		Command:     cmd,
		Constraints: DefaultConstraints,
	}
}

// CommandMap maps a process ProcessType to a Command.
type CommandMap map[ProcessType]Command

func commandMapFromProcfile(p procfile.Procfile) CommandMap {
	cm := make(CommandMap)
	for n, c := range p {
		cm[ProcessType(n)] = Command(c)
	}
	return cm
}

// Scan implements the sql.Scanner interface.
func (cm *CommandMap) Scan(src interface{}) error {
	h := hstore.Hstore{}
	if err := h.Scan(src); err != nil {
		return err
	}

	m := make(CommandMap)

	for k, v := range h.Map {
		m[ProcessType(k)] = Command(v.String)
	}

	*cm = m

	return nil
}

// Value implements the driver.Value interface.
func (cm CommandMap) Value() (driver.Value, error) {
	m := make(map[string]sql.NullString)

	for k, v := range cm {
		m[string(k)] = sql.NullString{
			Valid:  true,
			String: string(v),
		}
	}

	h := hstore.Hstore{
		Map: m,
	}

	return h.Value()
}

// Constraints aliases constraints.Constraints to implement the
// sql.Scanner interface.
type Constraints constraints.Constraints

func parseConstraints(con string) (*Constraints, error) {
	if con == "" {
		return nil, nil
	}

	if n, ok := NamedConstraints[con]; ok {
		return &n, nil
	}

	c, err := constraints.Parse(con)
	if err != nil {
		return nil, err
	}

	r := Constraints(c)
	return &r, nil
}

func (c *Constraints) UnmarshalJSON(b []byte) error {
	var s string

	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	cc, err := parseConstraints(s)
	if err != nil {
		return err
	}

	if cc != nil {
		*c = *cc
	}

	return nil
}

func (c Constraints) String() string {
	for n, constraint := range NamedConstraints {
		if c == constraint {
			return n
		}
	}

	return fmt.Sprintf("%d:%s", c.CPUShare, c.Memory)
}

// Formation maps a process ProcessType to a Process.
type Formation map[ProcessType]*Process

// NewFormation creates a new Formation based on an existing Formation and
// the available processes from a CommandMap.
func NewFormation(f Formation, cm CommandMap) Formation {
	processes := make(Formation)

	// Iterate through all of the available process types in the CommandMap.
	for t, cmd := range cm {
		p := NewProcess(t, cmd)

		if existing, found := f[t]; found {
			// If the existing Formation already had a process
			// configuration for this process type, copy over the
			// instance count.
			p.Quantity = existing.Quantity
			p.Constraints = existing.Constraints
		}

		processes[t] = p
	}

	return processes
}

// newFormation takes a slice of processes and returns a Formation.
func newFormation(p []*Process) Formation {
	f := make(Formation)

	for _, pp := range p {
		f[pp.Type] = pp
	}

	return f
}

// Processes takes a Formation and returns a slice of the processes.
func (f Formation) Processes() []*Process {
	var processes []*Process

	for _, p := range f {
		processes = append(processes, p)
	}

	return processes
}

// processesUpdate updates an existing process into the database.
func processesUpdate(db *gorm.DB, process *Process) error {
	return db.Save(process).Error
}
