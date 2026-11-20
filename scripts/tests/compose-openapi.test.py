#!/usr/bin/env python3
"""D3-S3 composition, reference and generation regressions."""
import copy
import importlib.util
import json
import sys
sys.dont_write_bytecode = True
from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]


def module(name):
    loader = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / f'{name}.py')
    result = importlib.util.module_from_spec(loader)
    loader.loader.exec_module(result)
    return result


compose = module('compose-openapi')
features = module('build-features-page')


class CompositionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base = yaml.safe_load((ROOT / compose.BASE).read_text())
        cls.part = yaml.safe_load((ROOT / compose.PART).read_text())

    def test_deterministic_generation_and_committed_outputs(self):
        first = {**compose.generated(), **features.generated()}
        self.assertEqual(first, {**compose.generated(), **features.generated()})
        for path, body in first.items():
            with self.subTest(path=path):
                self.assertEqual((ROOT / path).read_text(), body, 'run make inventory')

    def test_transitive_references_in_every_contract(self):
        compose.validate_refs(self.base)
        for body in compose.generated().values():
            compose.validate_refs(yaml.safe_load(body))
        # A missing definition reached through another definition must fail,
        # even when no operation refers directly to that missing definition.
        spec = {'paths': {'/': {'get': {'responses': {'200': {'$ref': '#/components/responses/OK'}}}}},
                'components': {'responses': {'OK': {'$ref': '#/components/schemas/Missing'}}}}
        with self.assertRaisesRegex(ValueError, 'unresolved reference'):
            compose.validate_refs(spec)

    def test_structural_overrides_and_escaped_pointer(self):
        spec = compose.compose(self.base, self.part)
        me = compose.operation(spec, 'getMe')
        props = compose.pointer(me, '/responses/200/content/application~1json/schema/properties')
        self.assertNotIn('home_site', props)
        host_props = compose.pointer(compose.operation(self.base, 'getMe'),
                                     '/responses/200/content/application~1json/schema/properties')
        self.assertIn('home_site', host_props)
        self.assertEqual(compose.pointer(spec, '/components/responses/TooLarge/description'),
                         'The request or deployment exceeds its applicable size limit.')
        # Every value override lands at its pointer in the product, and the
        # platform contract keeps its own text.
        for override in self.part['overrides']:
            if 'value' not in override:
                continue
            if 'component' in override:
                target, path = spec['components'], override['component']
            else:
                target, path = compose.operation(spec, override['op']), override['set']
            self.assertEqual(compose.pointer(target, path), override['value'])
        platform_metrics = compose.operation(self.base, 'getAdminPaperMetrics')
        product_metrics = compose.operation(spec, 'getAdminPaperMetrics')
        self.assertNotEqual(platform_metrics['parameters'][3]['description'],
                            product_metrics['parameters'][3]['description'])
        base, product, _ = features.feature_data()
        base_features = {f['id']: f for a in base['areas'] for f in a['features']}
        product_features = {f['id']: f for a in product['areas'] for f in a['features']}
        for feature, field, phrase in (
                ('site-versions', 'editions_note', 'small box keeps every version'),
                ('notify-secrets', 'editions_note', 'small-box installer'),
                ('deploy-archive', 'editions_note', 'small box has no total cap'),
                ('access-company-signin', 'limits', 'Google-based issuer needs an allowed-domain list'),
                ('access-named-viewers', 'editions_note', 'each site has its own address and visitors can sign in'),
                ('notify-webhook', 'editions_note', 'small box needs its sealing key set')):
            self.assertIn(phrase, base_features[feature][field])
            self.assertIn(phrase, product_features[feature][field])
            self.assertNotIn('Simple Hack', base_features[feature][field])
            self.assertIn('hack', product_features[feature][field].lower())
        self.assertEqual(base_features['addresses-families']['editions_note'],
                         'Hosted Simple Host only; Enterprise has no custom domains by decision.')
        self.assertIn('Returns nothing for event accounts.', base_features['showcase-feed']['limits'])
        part = copy.deepcopy(self.part)
        part['overrides'].append({'op': 'getMe', 'remove': '/missing'})
        with self.assertRaises(KeyError):
            compose.compose(self.base, part)
        part['overrides'][-1] = {'component': '/responses/Missing/description', 'value': 'missing'}
        with self.assertRaises(KeyError):
            compose.compose(self.base, part)

    def test_shared_selectors_require_exactly_one_operation(self):
        self.assertEqual(sum(' ' in item for item in self.part['include']), 10)
        self.assertIs(compose.operation(self.base, 'GET /llms.md'), self.base['paths']['/llms.md']['get'])
        for selector in ('missingOperation', 'GET /missing'):
            with self.assertRaisesRegex(ValueError, 'found 0'):
                compose.operation(self.base, selector)
        ambiguous = {'paths': {'/one': {'get': {'operationId': 'same'}},
                               '/two': {'get': {'operationId': 'same'}}}}
        with self.assertRaisesRegex(ValueError, 'found 2'):
            compose.operation(ambiguous, 'same')


if __name__ == '__main__':
    unittest.main()
