package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

type FileWriter struct {
	writerConfig *WriterConfig
}

// NewFileWriter returns a new FileWriter
func NewFileWriter(wc *WriterConfig) *FileWriter {
	return &FileWriter{
		writerConfig: wc,
	}
}
func (fr *FileWriter) Write(recChan chan Record) {
	logger := slog.With(slog.String("writer", FILE_WRITER_TYPE))
	f, err := os.Create(fr.writerConfig.FilePath)
	if err != nil {
		logger.Error(fmt.Sprintf("error while trying to open file: %v", err))
		os.Exit(1)
	}
	defer f.Close()
	recs := Records{}
	for rec := range recChan {
		recs = append(recs, rec)
	}

	// We cannot use the following line of code because it automatically replaces certain html characters
	// with the corresponding Unicode replacement rune.
	// recsJson, err := json.MarshalIndent(recs, "", "  ")
	// if err != nil {
	// 	log.Print(err.Error())
	// }
	// See
	// https://stackoverflow.com/questions/28595664/how-to-stop-json-marshal-from-escaping-and
	// https://developpaper.com/the-solution-of-escaping-special-html-characters-in-golang-json-marshal/
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(recs); err != nil {
		logger.Error(fmt.Sprintf("error while encoding records: %v", err))
		return
	}

	var indentBuffer bytes.Buffer
	if err := json.Indent(&indentBuffer, buffer.Bytes(), "", "  "); err != nil {
		logger.Error(fmt.Sprintf("error while indenting json: %v", err))
		return
	}
	if _, err = f.Write(indentBuffer.Bytes()); err != nil {
		logger.Error(fmt.Sprintf("error while writing json to file: %v", err))
	} else {
		logger.Info(fmt.Sprintf("wrote %d records to file %s", len(recs), fr.writerConfig.FilePath))
	}
}
