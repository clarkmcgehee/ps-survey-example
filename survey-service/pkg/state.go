package pkg

type PlayerState int

const (
	Stopped PlayerState = iota
	Playing
	Paused
)

func (p *PlayerState) Play() {
	switch *p {
	case Stopped:
		*p = Playing
	case Playing:
		return
	case Paused:
		*p = Playing
	}
}

func (p *PlayerState) Pause() {
	switch *p {
	case Stopped:
		*p = Paused
	case Playing:
		*p = Paused
	case Paused:
		*p = Stopped
	}
}

func (p *PlayerState) Stop() {
	switch *p {
	case Stopped:
		return
	case Playing:
		*p = Stopped
	case Paused:
		*p = Stopped
	}
}

func (p *PlayerState) State() string {
	switch *p {
	case Stopped:
		return "stop"
	case Playing:
		return "play"
	case Paused:
		return "pause"
	}
	return "unknown"
}

type KafkaState int

const (
	Disconnected KafkaState = iota
	Waiting
	Ready
	Consuming
)
