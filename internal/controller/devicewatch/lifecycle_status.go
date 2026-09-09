package devicewatch

func (d *LifecycleDriver) Active() bool {
	return d != nil && d.runtime != nil && d.runtime.Running()
}
