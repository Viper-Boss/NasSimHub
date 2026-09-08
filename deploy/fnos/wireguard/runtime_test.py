import base64
import copy
import unittest
from runtime import validate


class ValidationTests(unittest.TestCase):
    def setUp(self):
        self.config = dict(revision=1, enabled=True, address='10.77.0.1/24', listen_port=51820,
                           peers=[dict(public_key=base64.b64encode(bytes(range(32))).decode(), allowed_ips=['10.77.0.2/32'], endpoint='')])

    def test_valid(self):
        self.assertEqual(str(validate(self.config)), '10.77.0.1/24')

    def test_reject_dangerous_routes(self):
        for route in ['0.0.0.0/0', '192.168.120.0/24', '10.77.0.1/32', '10.77.0.255/32', '::/0']:
            with self.subTest(route=route), self.assertRaises(ValueError):
                self.config['peers'][0]['allowed_ips'] = [route]
                validate(self.config)

    def test_reject_injection(self):
        for value in ['host:51820\nPostUp=x', 'host;id:51820', 'host:0']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.config['peers'][0]['endpoint'] = value
                validate(self.config)

    def test_duplicate(self):
        self.config['peers'].append(copy.deepcopy(self.config['peers'][0]))
        with self.assertRaises(ValueError):
            validate(self.config)

    def test_zero_key(self):
        self.config['peers'][0]['public_key'] = base64.b64encode(bytes(32)).decode()
        with self.assertRaises(ValueError):
            validate(self.config)


if __name__ == '__main__':
    unittest.main()
