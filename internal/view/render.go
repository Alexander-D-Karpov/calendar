package view

type Rendered struct {
	Grid   *Grid
	Month  *MonthView
	Year   *YearView
	Agenda *AgendaView
}

func Render(p Period, items []Item, o Options) Rendered {
	switch p.Kind {
	case Month:
		return Rendered{Month: BuildMonth(p, items, o)}
	case Year:
		return Rendered{Year: BuildYear(p, items, o)}
	case Agenda:
		return Rendered{Agenda: BuildAgenda(p, items, o)}
	}
	return Rendered{Grid: BuildGrid(p, items, o)}
}
