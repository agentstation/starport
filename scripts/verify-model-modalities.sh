#!/usr/bin/env bash
# Guard for the model modalities campaign (plan prefix MMD). Each condition
# asserts one structural property of the target design:
#
#   - the canonical inference vocabulary names every modality a caller sends,
#     and both protocol codecs carry each part in and out,
#   - a request that a model cannot serve fails before any provider call,
#     and a media turn reports a real cost,
#   - the five dedicated media operations come from the catalog, and one
#     named operation set decides what routes,
#   - the media routes carry their own scopes, and an anonymous deployment
#     reaches them.
#
# It reports every condition and exits nonzero while any condition fails.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pass=0
fail=0

check() {
  local id="$1" desc="$2"
  shift 2
  if "$@" >/dev/null 2>&1; then
    printf 'PASS %s %s\n' "$id" "$desc"
    pass=$((pass + 1))
  else
    printf 'FAIL %s %s\n' "$id" "$desc"
    fail=$((fail + 1))
  fi
}

grep_q() { grep -Rq -- "$1" "${@:2}"; }

# in_tests reports that a term appears in a Go test file under the given
# package. The distinction matters: a constant that only the source names is
# a vocabulary entry, not a held contract.
in_tests() { grep -Rq --include='*_test.go' -- "$1" "${@:2}"; }

# all_present reports that every term before the -- appears somewhere under
# the paths after it. A partial vocabulary is the failure this catches: one
# modality lands, the other two stay behind, and a single-term grep passes.
all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  local term
  for term in "${terms[@]}"; do
    grep -Rq -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# --- Phase A, multimodal chat input ---

check MMD-V01 "the canonical content vocabulary names audio, document, and video parts" \
  all_present ContentAudio ContentDocument ContentVideo -- internal/inference

check MMD-V02 "a contract test asserts every content kind has a clone arm" \
  in_tests ContentDocument internal/inference

check MMD-V03 "the OpenAI codec carries the audio and file part spellings" \
  all_present input_audio 'file_data' 'filename' -- internal/protocol/openai

check MMD-V04 "the OpenRouter codec carries the audio and file part spellings" \
  all_present input_audio 'file_data' 'filename' -- internal/protocol/openrouter

check MMD-V05 "a connector contract test holds the non-text part payload" \
  grep_q ContentAudio internal/providers/connectors

check MMD-V06 "the planner refuses a part kind the target model cannot accept" \
  grep_q ErrModalityUnsupported internal/routing

check MMD-V07 "one estimator serves the routing metadata and the usage path" \
  grep_q EstimateMediaUnits internal/inference

check MMD-V08 "the response cache keys inline media bytes and skips a remote media URL" \
  grep_q ContentAudio internal/response/cache

check MMD-V09 "the oversize request body states the limit in bytes" \
  grep_q 'StatusRequestEntityTooLarge' internal/server

check MMD-V10 "the console audio control follows the model input modalities" \
  grep_q 'input_audio' console/src

# --- Phase B, non-text chat output ---

check MMD-V11 "Starmap prices audio input and audio output" \
  all_present audio_input audio_output -- internal/catalog

check MMD-V12 "the canonical chat types name an output modality and a delta audio chunk" \
  all_present 'type Modality' 'Audio' -- internal/inference/types.go

check MMD-V13 "both codecs read the output modality request field" \
  all_present 'json:"modalities"' -- internal/protocol/openai internal/protocol/openrouter

check MMD-V14 "a response holding an image part encodes to the documented field" \
  all_present 'images' ContentImage -- internal/protocol/openai

check MMD-V15 "an unpriced media turn records a named cost reason and no cost" \
  grep_q CostReasonMediaUnpriced internal/inference

check MMD-V16 "the spend budget counts a priced media turn" \
  grep_q MediaUnits internal/limits

# --- Phase C, dedicated media operations ---

check MMD-V17 "the catalog projection names the five media operations" \
  all_present images-generations images-edits audio-speech \
  audio-transcriptions audio-translations -- internal/catalog

check MMD-V18 "the residual offerings with no operation have a stated reason" \
  test -f docs/proof/model-modalities/mod12.md

check MMD-V19 "one named operation set replaces the hardcoded guards" \
  grep_q 'OperationSet' internal/routing

check MMD-V20 "the route candidates are filtered to the requested operation" \
  grep_q ServesOperation internal/routing

check MMD-V21 "the server registers the five OpenAI media paths" \
  all_present '/v1/images/generations' '/v1/images/edits' '/v1/audio/speech' \
  '/v1/audio/transcriptions' '/v1/audio/translations' -- internal/server/routes.go

check MMD-V22 "the media scopes exist and the anonymous identity carries them" \
  all_present 'images:write' 'audio:write' -- internal/identity/anonymous.go

check MMD-V23 "a transcription upload is read as multipart form data" \
  grep_q 'ParseMultipartForm' internal/server/controllers

check MMD-V24 "the console model facet reads output modalities" \
  grep_q 'output_modalities' console/src/lib/modelFilter.ts

# --- Close ---

check MMD-V25 "CI runs this gate" \
  grep_q 'verify-model-modalities.sh' .github/workflows

check MMD-V26 "the required evidence list names this gate and its terminal count" \
  grep_q 'verify-model-modalities.sh' AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
