// Пакет platform прячет всё привилегированное и платформенное от остального кода.
package platform

import "ampersand/internal/platform/core"

type Check = core.Check
type Probe = core.Probe
type Options = core.Options
type Runtime = core.Runtime
type Controller = core.Controller
type EngineManifest = core.EngineManifest

var (
	ReadManifest    = core.ReadManifest
	VerifyFile      = core.VerifyFile
	EngineBinary    = core.EngineBinary
	MissingArgFiles = core.MissingArgFiles
)
