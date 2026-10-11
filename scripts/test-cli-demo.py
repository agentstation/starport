#!/usr/bin/env python3
"""Verify credential redaction at the CLI capture boundary."""
from pathlib import Path
import sys
import unittest
import tempfile
import json

sys.path.insert(0, str(Path(__file__).resolve().parent / "readme-demo"))
from capture_cli import safe_banner, selected_model, MODEL, PROVIDER_MODEL
import importlib.util

spec = importlib.util.spec_from_file_location("recorder", Path(__file__).resolve().parent / "record-cli-demo.py")
recorder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recorder)


class BannerTests(unittest.TestCase):
    def test_redacts_gateway_key_and_console_launch_credential(self):
        banner = safe_banner("Starport development gateway\nURL: http://127.0.0.1:7827\n"
                             "Authentication: required\nGateway API key (shown once): secret-key\n"
                             "Console (one-time launch link): http://127.0.0.1:7827/launch?ticket=secret-ticket\n")
        self.assertNotIn("secret-key", banner)
        self.assertNotIn("secret-ticket", banner)
        self.assertEqual(banner.count("[value hidden]"), 2)
        self.assertIn("Authentication: required", banner)
        self.assertIn("URL: http://127.0.0.1:7827", banner)

    def test_omits_startup_logs_even_when_they_contain_credentials(self):
        self.assertEqual(safe_banner("DEBUG key=secret-key\nDEBUG ticket=secret-ticket\n"), "")


class OfferingTests(unittest.TestCase):
    def discovery(self, provider="openai", provider_model=PROVIDER_MODEL, operations=None, canonical=MODEL):
        return {"models": [{"id": canonical, "offerings": [{"provider": provider,
                "provider_model_id": provider_model, "operations": operations or ["chat-completions"]}]}]}

    def test_requires_exact_canonical_and_provider_model(self):
        model, offering = selected_model(self.discovery())
        self.assertEqual(model['id'], 'openai/gpt-6.1-sol')
        self.assertEqual(offering['provider_model_id'], 'gpt-6.1-sol')

    def test_rejects_a_different_provider_model(self):
        with self.assertRaisesRegex(RuntimeError, 'exact OpenAI provider model'):
            selected_model(self.discovery(provider_model='gpt-6.1-sol-preview'))

    def test_rejects_a_different_provider(self):
        with self.assertRaisesRegex(RuntimeError, 'exact OpenAI provider model'):
            selected_model(self.discovery(provider='openrouter'))

    def test_rejects_a_different_canonical_model(self):
        with self.assertRaisesRegex(RuntimeError, 'canonical model is absent'):
            selected_model(self.discovery(canonical='openai/gpt-6.1-sol-preview'))

    def test_requires_the_declared_chat_operation(self):
        with self.assertRaisesRegex(RuntimeError, 'does not declare chat completions'):
            selected_model(self.discovery(operations=['responses']))


class CatalogBindingTests(unittest.TestCase):
    def fixture(self, directory):
        source = Path(directory)
        catalog = source / 'internal/embedded/catalog'
        files = {'generation.json': json.dumps({'generation_id': 'test-generation', 'payload': {'checksum': 'test-payload'}}),
                 'authors/openai/models/gpt-6.1-sol.yaml': 'author-model-test-input',
                 'providers/openai/models/gpt-6.1-sol.yaml': 'provider-model-test-input'}
        for name, text in files.items():
            path = catalog / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        binary = source / 'test-binary'
        binary.write_bytes(b'\0'.join(text.encode() for text in files.values()))
        return source, binary

    def test_binds_manifest_and_both_selected_model_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            source, binary = self.fixture(directory)
            evidence = recorder.catalog_inputs(source, binary)
            self.assertTrue(evidence['manifest_and_selected_model_inputs_embedded'])
            self.assertEqual(evidence['input_count'], 3)
            self.assertEqual(evidence['manifest']['generation_id'], 'test-generation')

    def test_rejects_a_binary_without_the_selected_catalog_input(self):
        with tempfile.TemporaryDirectory() as directory:
            source, binary = self.fixture(directory)
            binary.write_bytes(b'stale binary')
            with self.assertRaisesRegex(RuntimeError, 'does not embed this catalog input'):
                recorder.catalog_inputs(source, binary)

    def test_rejects_model_input_changes_after_build(self):
        with tempfile.TemporaryDirectory() as directory:
            source, binary = self.fixture(directory)
            (source / 'internal/embedded/catalog/providers/openai/models/gpt-6.1-sol.yaml').write_text('changed-input')
            with self.assertRaisesRegex(RuntimeError, 'does not embed this catalog input'):
                recorder.catalog_inputs(source, binary)

    def test_inventory_changes_when_another_catalog_input_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            source, binary = self.fixture(directory)
            before = recorder.catalog_inputs(source, binary)
            (source / 'internal/embedded/catalog/provenance.yaml').write_text('test-provenance-input')
            after = recorder.catalog_inputs(source, binary)
            self.assertNotEqual(before['input_inventory_sha256'], after['input_inventory_sha256'])

    def test_transcript_discloses_reviewed_local_build_and_exact_ids(self):
        catalog = {'starmap_source_commit': 'test-commit', 'catalog_inputs_modified': True, 'manifest': {'generation_id': 'test-generation',
                   'payload': {'checksum': 'test-payload'}}, 'manifest_sha256': 'test-manifest',
                   'input_inventory_sha256': 'test-inventory'}
        text = recorder.transcript_text('test command output\n', catalog)
        self.assertIn('not a new public release', text)
        self.assertIn('uncommitted local changes', text)
        self.assertIn('GOWORK=off go -C', text)
        self.assertIn('--starmap-source "$starmap_source"', text)
        self.assertIn('`openai/gpt-6.1-sol`', text)
        self.assertIn('`gpt-6.1-sol`', text)


class TapeTypingTests(unittest.TestCase):
    def test_svg_with_only_complete_commands_fails(self):
        commands = ["starport models search gpt-6.1-sol", "starport models show openai/gpt-6.1-sol",
                    "starport dev --no-open >gateway.log 2>&1 &"]
        svg = '<svg xmlns="http://www.w3.org/2000/svg">' + ''.join(
            '<text>$ ' + command.replace('&', '&amp;') + '</text>' for command in commands) + '</svg>'
        with self.assertRaisesRegex(RuntimeError, "too few typing frames"):
            recorder.verify_typing(svg)

    def test_svg_needs_intermediate_prefixes_for_each_command(self):
        commands = ["starport models search gpt-6.1-sol", "starport models show openai/gpt-6.1-sol",
                    "starport dev --no-open >gateway.log 2>&1 &"]
        rows = []
        for command in commands:
            for length in range(len(command) - 8, len(command)):
                rows.append('<text>$ ' + command[:length].replace('&', '&amp;') + '</text>')
        svg = '<svg xmlns="http://www.w3.org/2000/svg">' + ''.join(rows) + '</svg>'
        result = recorder.verify_typing(svg)
        self.assertEqual(result['milliseconds_per_character'], 35)
        self.assertEqual(list(result['svg_prefix_counts'].values()), [8, 8, 6])

    def test_tape_types_commands_in_an_actual_shell(self):
        tape = (Path(__file__).resolve().parent / 'cli-demo.tape').read_text()
        commands = recorder.tape_commands(tape)
        self.assertIn('starport models show openai/gpt-6.1-sol', commands)
        self.assertIn('Set TypingSpeed 35ms', tape)
        self.assertIn('Wait+Line@10s /^[$]$/', tape)
        self.assertNotIn('exec /usr/bin/sandbox-exec', tape)


class ChapterTests(unittest.TestCase):
    def svg(self, lines):
        return '<svg xmlns="http://www.w3.org/2000/svg">' + ''.join(
            f'<g transform="translate({index},0)"><text>' + line.replace('&', '&amp;') + '</text></g>'
            for index, line in enumerate(lines)) + '</svg>'

    def test_titles_precede_actions_and_client_card_closes(self):
        lines = [recorder.OPENING]
        for title, command in recorder.CHAPTERS:
            lines.extend([title, '$ ' + command])
        lines.append(recorder.CLOSING)
        story = recorder.verify_titles(self.svg(lines))
        self.assertEqual(len(story['chapters']), 3)
        self.assertEqual([chapter['hold_seconds'] for chapter in story['chapters']], [3, 2, 2])

    def test_title_after_action_fails(self):
        lines = [recorder.OPENING]
        for title, command in recorder.CHAPTERS:
            lines.extend(['$ ' + command, title])
        lines.append(recorder.CLOSING)
        with self.assertRaisesRegex(RuntimeError, 'must precede'):
            recorder.verify_titles(self.svg(lines))

    def test_hidden_gateway_setup_in_visible_svg_fails(self):
        with self.assertRaisesRegex(RuntimeError, 'hidden gateway setup commands'):
            recorder.verify_titles(self.svg(['$ demo-control ready']))

    def test_goal_after_actions_fails(self):
        lines = []
        for title, command in recorder.CHAPTERS:
            lines.extend([title, '$ ' + command])
        lines.extend([recorder.OPENING, recorder.CLOSING])
        with self.assertRaisesRegex(RuntimeError, 'goal must precede'):
            recorder.verify_titles(self.svg(lines))

    def test_tape_keeps_gateway_live_until_visible_result(self):
        result = recorder.verify_story_tape(recorder.TAPE.read_text())
        self.assertTrue(result['cleanup_after_visible_result'])
        self.assertTrue(result['live_gateway_checked_before_result'])

    def test_cleanup_before_result_fails(self):
        tape = recorder.TAPE.read_text()
        cleanup = next(command for command in recorder.tape_commands(tape) if command.startswith('kill -INT'))
        tape = tape.replace('Type `' + cleanup + '`', '')
        tape = tape.replace('Type `demo-control closing`', 'Type `' + cleanup + '`\nType `demo-control closing`')
        with self.assertRaisesRegex(RuntimeError, 'must precede cleanup'):
            recorder.verify_story_tape(tape)

    def test_next_action_stops_the_current_session_before_restart(self):
        commands = recorder.tape_commands(recorder.TAPE.read_text())
        ending = next(command for command in commands if recorder.CLOSING in command)
        self.assertLess(ending.index('stop this development session'), ending.index('export OPENAI_API_KEY='))
        self.assertLess(ending.index('export OPENAI_API_KEY='), ending.index('Restart: starport dev --no-open'))

    def test_hidden_result_fails(self):
        tape = recorder.TAPE.read_text().replace('Show\nSleep 8s', 'Sleep 8s')
        with self.assertRaisesRegex(RuntimeError, 'must remain visible'):
            recorder.verify_story_tape(tape)


if __name__ == "__main__":
    unittest.main()
