package reservation

func (e Evidence) valid() bool {
	return validID(e.ID) && e.Tokens >= 0 && (!e.NoCharge || (len(e.Quantities) == 0 && e.Tokens == 0))
}

func (a Attempt) evidenceAmount(e *Evidence) (int64, error) {
	if e == nil || !e.valid() {
		return 0, ErrInvalid
	}
	if e.NoCharge {
		return 0, nil
	}
	return a.amount(e.Quantities)
}
