package pkg

type playerState int

const (
	Stopped playerState = iota
	Playing
	Paused
)

func (p *playerState) Play() {
	switch *p {
	case Stopped:
		*p = Playing
	case Playing:
		return
	case Paused:
		*p = Playing
	}
}

func (p *playerState) Pause() {
	switch *p {
	case Stopped:
		*p = Paused
	case Playing:
		*p = Paused
	case Paused:
		*p = Stopped
	}
}

func (p *playerState) Stop() {
	switch *p {
	case Stopped:
		return
	case Playing:
		*p = Stopped
	case Paused:
		*p = Stopped
	}
}

func (p playerState) State() string {
	switch p {
	case Stopped:
		return "stop"
	case Playing:
		return "play"
	case Paused:
		return "pause"
	}
	return "unknown"
}

type kafkaState int

const (
	Waiting kafkaState = iota
	Ready
	Producing
)
