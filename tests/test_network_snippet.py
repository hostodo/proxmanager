import os
import tempfile
import unittest

from ruamel.yaml import YAML


class NetworkSnippetTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        os.environ['API_TOKEN'] = 'test-token'
        os.environ['DEFAULT_GATEWAY'] = '192.0.2.1'
        os.environ['SNIPPETS_DIR'] = self.tmp.name

        import app

        self.app_module = app
        self.client = app.app.test_client()

    def post_network(self, body):
        return self.client.post(
            '/node/snippets/network/14262',
            json=body,
            headers={'Authorization': 'Bearer test-token'},
        )

    def test_ipv4_only_network_snippet_does_not_require_ipv6(self):
        response = self.post_network({
            'ipv4_addresses': [{'address': '66.187.7.58', 'gateway': '66.187.7.1'}],
            'ipv6_addresses': [],
            'network_device_name': 'eth0',
            'is_centos': False,
            'mac_address': 'BC:24:11:53:E5:A4',
        })

        self.assertEqual(response.status_code, 200)
        data = response.get_json()
        with open(data['file_path']) as fh:
            config = YAML().load(fh)
        ethernet = config['ethernets']['eth0']
        self.assertEqual(ethernet['gateway4'], '66.187.7.1')
        self.assertNotIn('gateway6', ethernet)
        self.assertNotIn({'to': '::/0', 'via': None, 'on-link': True}, ethernet.get('routes', []))

    def test_dual_stack_network_snippet_keeps_ipv6_gateway_and_routes(self):
        response = self.post_network({
            'ipv4_addresses': [{'address': '66.187.7.58', 'gateway': '66.187.7.1'}],
            'ipv6_addresses': [{'address': '2606:65c0:40::1234', 'gateway': '2606:65c0:40::1'}],
            'network_device_name': 'eth0',
            'is_centos': False,
            'mac_address': 'BC:24:11:53:E5:A4',
        })

        self.assertEqual(response.status_code, 200)
        data = response.get_json()
        ethernet = data['config']['ethernets']['eth0']
        self.assertEqual(ethernet['gateway6'], '2606:65c0:40::1')
        self.assertIn({'to': '::/0', 'on-link': True, 'via': '2606:65c0:40::1'}, ethernet['routes'])


if __name__ == '__main__':
    unittest.main()
