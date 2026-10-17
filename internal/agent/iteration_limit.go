package agent

import (
	"errors"

	"github.com/cloudwego/eino/adk"
)

// iterationLimitNotice ends a run that used up its model-call budget.
const iterationLimitNotice = "\n\n> 已达到本轮的步数上限。已完成的操作都已生效，没有被撤销。想接着做，回复「继续」即可。"

// hitIterationLimit reports whether err is Eino's "ran out of model calls"
// error. It is not a failure of the work: everything the run did (files
// written, commands run) already happened, so surfacing it as an error would
// make a nearly finished task look broken.
func hitIterationLimit(err error) bool {
	return errors.Is(err, adk.ErrExceedMaxIterations)
}
