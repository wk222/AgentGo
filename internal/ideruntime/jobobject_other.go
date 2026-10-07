//go:build !windows

package ideruntime

type jobObjectManager struct{}

func newJobObjectManager() (*jobObjectManager, error) {
	return &jobObjectManager{}, nil
}

func (j *jobObjectManager) AssignProcess(pid int) error {
	return nil
}

func (j *jobObjectManager) Close() error {
	return nil
}
