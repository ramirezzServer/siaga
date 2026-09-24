package riversnap

import (
	"os"

	"github.com/ramirezzServer/siaga/services/ingest/internal/domain/riversnap"
)

func readRepoStations() ([]riversnap.Station, error) {
	f, err := os.Open("../../../../../docs/calibration/titik-sungai-32.csv")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadStations(f)
}
