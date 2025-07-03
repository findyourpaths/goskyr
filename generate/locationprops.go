package generate

import (
	"fmt"
	"hash/crc32"
	"sort"

	"github.com/findyourpaths/goskyr/utils"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type locationProps struct {
	path      path
	attr      string
	textIndex int // this will translate into child index within scrape.ElementLocation
	count     int
	examples  []string
	selected  bool
	color     tcell.Color
	distance  float64
	name      string
	iStrip    int // this is needed for the squashLocationManager function
	isText    bool
}

func makeLocationProps(nodePath path, example string, isText bool) locationProps {
	// slog.Debug("makeLocationProps()", "nodePath.actualstring()", nodePath.string(), "example", example)
	p := make([]node, len(nodePath))
	copy(p, nodePath)
	return locationProps{
		path:     p,
		examples: []string{example},
		count:    1,
		isText:   isText,
	}
}

func (lp locationProps) DebugString() string {
	p := lp.path.string()
	if len(p) > 100 {
		p = p[0:20] + "... ..." + p[len(p)-75:len(p)]
	}
	return fmt.Sprintf("{count: %d, name: %q, attr: %q, isText: %t, path.string()(%d): %q}", lp.count, lp.name, lp.attr, lp.isText, len(lp.path), p)
}

type locationManager []*locationProps

func (l locationManager) setColors() {
	if len(l) == 0 {
		return
	}
	for i, e := range l {
		if i != 0 {
			e.distance = l[i-1].distance + l[i-1].path.distance(e.path)
		}
	}
	// scale to 1 and map to rgb
	maxDist := l[len(l)-1].distance * 1.2
	s := 0.73
	v := 0.96
	for _, e := range l {
		h := e.distance / maxDist
		r, g, b := utils.HSVToRGB(h, s, v)
		e.color = tcell.NewRGBColor(r, g, b)
	}
}

func (l locationManager) setFieldNames(modelName, wordsDir string) error {
	hashes := map[uint32]string{}
	if modelName == "" {
		for _, e := range l {
			pathStr := e.path.string()
			hash := crc32.ChecksumIEEE([]byte(pathStr))
			if prev, found := hashes[hash]; found && prev != pathStr {
				panic(fmt.Sprintf("Detected duplicate hash %q for field %q", hash, e.path.string()))
			}
			hashes[hash] = pathStr
			e.name = fmt.Sprintf("F%x-%s-%d", hash, e.attr, e.textIndex)
		}
		sort.Slice(l, func(i, j int) bool { return l[i].name < l[j].name })
		return nil
	}

	// ll, err := ml.LoadLabler(modelName, wordsDir)
	// if err != nil {
	// 	return err
	// }
	// for _, e := range l {
	// 	pred, err := ll.PredictLabel(e.examples...)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	e.name = pred // TODO: if label has occured already, add index (eg text-1, text-2...)
	// }
	return nil
}

func (l locationManager) selectFieldsTable() {
	app := tview.NewApplication()
	table := tview.NewTable().SetBorders(true)
	cols, rows := 5, len(l)+1
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			color := tcell.ColorWhite
			if c < 1 || r < 1 {
				if c < 1 && r > 0 {
					color = tcell.ColorGreen
					table.SetCell(r, c, tview.NewTableCell(fmt.Sprintf("[%d] %s", r-1, l[r-1].name)).
						SetTextColor(color).
						SetAlign(tview.AlignCenter))
				} else if r == 0 && c > 0 {
					color = tcell.ColorBlue
					table.SetCell(r, c, tview.NewTableCell(fmt.Sprintf("example [%d]", c-1)).
						SetTextColor(color).
						SetAlign(tview.AlignCenter))
				} else {
					table.SetCell(r, c,
						tview.NewTableCell("").
							SetTextColor(color).
							SetAlign(tview.AlignCenter))
				}
			} else {
				var ss string
				if len(l[r-1].examples) >= c {
					ss = utils.ShortenString(l[r-1].examples[c-1], 40)
				}
				table.SetCell(r, c,
					tview.NewTableCell(ss).
						SetTextColor(l[r-1].color).
						SetAlign(tview.AlignCenter))
			}
		}
	}
	table.SetSelectable(true, false)
	table.Select(1, 1).SetFixed(1, 1).SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			app.Stop()
		}
		if key == tcell.KeyEnter {
			table.SetSelectable(true, false)
		}
	}).SetSelectedFunc(func(row int, column int) {
		l[row-1].selected = !l[row-1].selected
		if l[row-1].selected {
			table.GetCell(row, 0).SetTextColor(tcell.ColorRed)
			for i := 1; i < 5; i++ {
				table.GetCell(row, i).SetTextColor(tcell.ColorOrange)
			}
		} else {
			table.GetCell(row, 0).SetTextColor(tcell.ColorGreen)
			for i := 1; i < 5; i++ {
				table.GetCell(row, i).SetTextColor(l[row-1].color)
			}
		}
	})
	button := tview.NewButton("Hit Enter to generate config").SetSelectedFunc(func() {
		app.Stop()
	})

	grid := tview.NewGrid().SetRows(-11, -1).SetColumns(-1, -1, -1).SetBorders(false).
		AddItem(table, 0, 0, 1, 3, 0, 0, true).
		AddItem(button, 1, 1, 1, 1, 0, 0, false)
	grid.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			if button.HasFocus() {
				app.SetFocus(table)
			} else {
				app.SetFocus(button)
			}
			return nil
		}
		return event
	})

	if err := app.SetRoot(grid, true).SetFocus(grid).Run(); err != nil {
		panic(err)
	}
}
