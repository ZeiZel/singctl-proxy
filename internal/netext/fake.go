package netext

// FakeController is an in-memory Controller for tests in this and other packages
// (e.g. the Executor wiring). Set Present to simulate an installed+approved
// extension; Adds/Removes record the calls in order for assertions.
type FakeController struct {
	Present bool
	set     *targetSet
	Adds    []string
	Removes []string
}

// NewFake returns a FakeController; present seeds Available().
func NewFake(present bool) *FakeController {
	return &FakeController{Present: present, set: newTargetSet()}
}

func (f *FakeController) Available() bool { return f.Present }

func (f *FakeController) AddTarget(bundleID string) error {
	f.Adds = append(f.Adds, bundleID)
	f.set.add(bundleID)
	return nil
}

func (f *FakeController) RemoveTarget(bundleID string) error {
	f.Removes = append(f.Removes, bundleID)
	f.set.remove(bundleID)
	return nil
}

func (f *FakeController) Targets() []string { return f.set.list() }
