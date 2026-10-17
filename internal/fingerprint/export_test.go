package fingerprint

import "ytmusic/internal/throttle"

func ThrottleOf(c *AcoustIDClient) *throttle.Throttle { return c.api.Throttle }

func SetThrottle(c *AcoustIDClient, t *throttle.Throttle) { c.api.Throttle = t }
