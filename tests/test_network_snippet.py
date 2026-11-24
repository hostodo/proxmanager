import os
import tempfile
import unittest
from unittest.mock import patch

from ruamel.yaml import YAML


class NetworkSnippetTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        settings = patch.dict(os.environ, API_TOKEN='test-token', SNIPPETS_DIR=self.tmp.name)
        settings.start()
        self.addCleanup(settings.stop)

        import app

        self.app_module = app
        self.client = app.app.test_client()

    def post_network(self, body):
        return self.client.post(
            '/node/snippets/network/14262',
            json=body,
            headers={'Authorization': 'Bearer test-token'},
        )

    def read_ethernet(self, response):
        self.assertEqual(response.status_code, 200)
        data = response.get_json()
        with open(data['file_path']) as fh:
            config = YAML(typ='safe').load(fh)
        self.assertEqual(config, data['config'])
        self.assertEqual(config['version'], 2)
        return config['ethernets']['eth0']

    def test_ipv4_only_network_snippet_does_not_require_ipv6(self):
        response = self.post_network({
            'ipv4_addresses': [{'address': '66.187.7.58', 'gateway': '66.187.7.1'}],
            'ipv6_addresses': [],
            'network_device_name': 'eth0',
            'is_centos': False,
            'mac_address': 'BC:24:11:53:E5:A4',
        })

        ethernet = self.read_ethernet(response)
        self.assertEqual(ethernet['gateway4'], '66.187.7.1')
        self.assertNotIn('gateway6', ethernet)
        self.assertEqual(ethernet['routes'], [{'to': '0.0.0.0/0', 'via': '66.187.7.1'}])

    def test_centos_ipv4_only_network_snippet_omits_manual_routes(self):
        response = self.post_network({
            'ipv4_addresses': [{'address': '66.187.7.58', 'gateway': '66.187.7.1'}],
            'network_device_name': 'eth0',
            'is_centos': True,
            'mac_address': 'BC:24:11:53:E5:A4',
        })

        ethernet = self.read_ethernet(response)
        self.assertEqual(ethernet['gateway4'], '66.187.7.1')
        self.assertNotIn('gateway6', ethernet)
        self.assertNotIn('routes', ethernet)

    def test_dual_stack_snippet_uses_one_default_per_family_and_an_exact_gateway_host_route(self):
        response = self.post_network({
            'ipv4_addresses': [{'address': '192.0.2.58', 'gateway': '192.0.2.1'}],
            'ipv6_addresses': [{'address': '2001:db8:1::1234', 'gateway': '2001:db8:2::1'}],
            'network_device_name': 'eth0',
            'is_centos': False,
            'explicit_default_routes': True,
            'mac_address': 'BC:24:11:53:E5:A4',
        })

        ethernet = self.read_ethernet(response)
        self.assertEqual(ethernet['match'], {'macaddress': 'BC:24:11:53:E5:A4'})
        self.assertEqual(ethernet['addresses'], ['192.0.2.58/24', '2001:db8:1::1234/64'])
        self.assertNotIn('gateway4', ethernet)
        self.assertNotIn('gateway6', ethernet)
        self.assertEqual(ethernet['routes'], [
            {'to': '2001:db8:2::1/128', 'via': '::'},
            {'to': '::/0', 'via': '2001:db8:2::1'},
            {'to': '0.0.0.0/0', 'via': '192.0.2.1'},
        ])

    def test_dual_stack_legacy_format_keeps_existing_alpine_routes(self):
        ipv6_gateway = '2001:db8:2::1'
        request = {
            'ipv4_addresses': [{'address': '192.0.2.58', 'gateway': '192.0.2.1'}],
            'ipv6_addresses': [{'address': '2001:db8:1::1234', 'gateway': ipv6_gateway}],
            'network_device_name': 'eth0',
            'is_centos': False,
            'mac_address': 'BC:24:11:53:E5:A4',
        }
        for payload in (request, dict(request, explicit_default_routes=False)):
            with self.subTest(payload=payload):
                ethernet = self.read_ethernet(self.post_network(payload))
                self.assertEqual(ethernet['gateway4'], '192.0.2.1')
                self.assertEqual(ethernet['gateway6'], ipv6_gateway)
                self.assertEqual(ethernet['routes'], [
                    {'to': ipv6_gateway, 'via': '::', 'on-link': True},
                    {'to': '::/0', 'via': ipv6_gateway, 'on-link': True},
                    {'to': '0.0.0.0/0', 'via': '192.0.2.1'},
                ])

    def test_ipv4_only_explicit_format_omits_gateway_fields(self):
        ethernet = self.read_ethernet(self.post_network({
            'ipv4_addresses': [{'address': '192.0.2.58', 'gateway': '192.0.2.1'}],
            'explicit_default_routes': True,
            'mac_address': 'BC:24:11:53:E5:A4',
        }))
        self.assertNotIn('gateway4', ethernet)
        self.assertNotIn('gateway6', ethernet)
        self.assertEqual(ethernet['routes'], [{'to': '0.0.0.0/0', 'via': '192.0.2.1'}])

    def test_explicit_routes_rejects_invalid_flag_and_centos_combination(self):
        for value, is_centos in (('true', False), (1, False), (None, False), (True, True)):
            with self.subTest(value=value, is_centos=is_centos):
                response = self.post_network({
                    'ipv4_addresses': [{'address': '192.0.2.58', 'gateway': '192.0.2.1'}],
                    'explicit_default_routes': value,
                    'is_centos': is_centos,
                    'mac_address': 'BC:24:11:53:E5:A4',
                })
                self.assertEqual(response.status_code, 400)
                self.assertEqual(os.listdir(self.tmp.name), [])

    def test_centos_dual_stack_keeps_gateway_fields_without_manual_routes(self):
        response = self.post_network({
            'ipv4_addresses': [{'address': '192.0.2.58', 'gateway': '192.0.2.1'}],
            'ipv6_addresses': [{'address': '2001:db8:1::1234', 'gateway': '2001:db8:2::1'}],
            'is_centos': True,
            'mac_address': 'BC:24:11:53:E5:A4',
        })

        ethernet = self.read_ethernet(response)
        self.assertEqual(ethernet['gateway4'], '192.0.2.1')
        self.assertEqual(ethernet['gateway6'], '2001:db8:2::1')
        self.assertNotIn('routes', ethernet)


if __name__ == '__main__':
    unittest.main()
