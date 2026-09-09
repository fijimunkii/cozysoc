package secretstore

import "fmt"

func NewHeadless() (Store, error) {
	return nil, fmt.Errorf("%w: headless storage requires an explicitly provisioned machine-bound backend", ErrUnavailable)
}
