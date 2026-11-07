package assets

import _ "embed"

//go:embed squawk.jpg
var Seagull []byte

const SeagullName = "squawk.jpg"
const SeagullType = "image/jpeg"
