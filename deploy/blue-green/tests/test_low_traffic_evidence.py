"""Protect opt-in delivery evidence from missing, partial, or unrelated outcomes."""
import csv
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('low_traffic', Path(__file__).resolve().parents[1] / 'low-traffic-evidence.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class LowTrafficEvidenceTest(unittest.TestCase):
    def check(self, mutation=None, natural_successes=0, reason='coverage_gap', natural_requests=0, unresolved=0, http_error=False, http_counts=None, rejections=None, reviewed=""):
        """Build complete, candidate-correlated fixtures and vary one contract."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'http-observations.json').write_text(json.dumps({'post': http_counts if http_counts is not None else ({'/v1/messages:http_400':1} if http_error else {}), 'policy_rejections': {'post': rejections or []}}))
            probes, logs = [], []
            for protocol in ('chat', 'responses'):
                for stream in (False, True):
                    rid = protocol + str(stream)
                    path = '/v1/chat/completions' if protocol == 'chat' else '/v1/responses'
                    probes.append(dict(protocol=protocol, stream=stream, http=200, text='OK', terminal=True,
                                       final=[dict(type=2, request_id=rid, stream_status={'status':'ok','billing_finalization':'settled'} if stream else None)]))
                    logs.append(f'[GIN] fixture | relay | {rid} | 200 | 1ms | local | POST {path}')
            if mutation:
                mutation(probes, logs)
            (root/'probes.json').write_text(json.dumps(probes))
            (root/'candidate.log').write_text('\n'.join(logs))
            for name, headers, rows in (
                ('comparison.tsv', ['model_evidence','result','reason'], [['no_comparable_traffic','inconclusive',reason]]),
                ('final-request-success.tsv', ['window','final_requests','successes','conversion_errors'], [['post',natural_requests,natural_successes,0]]),
                ('final-request-coverage.tsv', ['unresolved_requests'], [[unresolved]]),
            ):
                with (root/name).open('w') as handle:
                    writer=csv.writer(handle,delimiter='\t');writer.writerow(headers);writer.writerows(rows)
            return module.validate(root/'probes.json',root/'candidate.log',root,reviewed)

    def test_complete_candidate_probes_allow_empty_natural_traffic(self):
        self.assertTrue(self.check())

    def test_probes_must_be_from_candidate_and_complete(self):
        self.assertFalse(self.check(lambda probes, logs: logs.clear()))
        self.assertFalse(self.check(lambda probes, logs: probes.pop()))
        self.assertFalse(self.check(lambda probes, logs: probes[0].update(text='')))

    def test_failed_or_partial_stream_is_not_delivery_evidence(self):
        self.assertFalse(self.check(lambda probes, logs: probes[1].update(terminal=False)))
        self.assertFalse(self.check(lambda probes, logs: probes[1]['final'][0]['stream_status'].update(billing_finalization='partial')))

    def test_rate_uncertainty_is_not_relabelled_as_low_traffic(self):
        self.assertFalse(self.check(reason='uncertain_rate_drop'))

    def test_inconsistent_natural_outcomes_fail(self):
        self.assertFalse(self.check(natural_successes=1))
        self.assertFalse(self.check(natural_requests=1))
        self.assertFalse(self.check(unresolved=1))
        self.assertFalse(self.check(http_error=True))

    def test_only_reviewed_correlated_policy_denials_are_separate(self):
        row = dict(request_id="deny", path="/v1/messages", status=403, reason="auth_group_not_allowed")
        args = dict(http_counts={"/v1/messages:http_403": 1}, rejections=[row])
        self.assertFalse(self.check(**args))
        self.assertTrue(self.check(**args, reviewed="auth_group_not_allowed"))
        self.assertFalse(self.check(http_counts=args["http_counts"], reviewed="auth_group_not_allowed"))
        self.assertFalse(self.check(**args, reviewed="auth_ip_not_allowed"))
        self.assertFalse(self.check(**args, reviewed="invented_reason"))
        self.assertFalse(self.check(**args, reviewed="auth_group_not_allowed", unresolved=1))
        self.assertFalse(self.check(**args, reviewed="auth_group_not_allowed", natural_requests=1))

    def test_rejection_evidence_does_not_hide_other_statuses_or_duplicate_counts(self):
        row = dict(request_id="deny", path="/v1/messages", status=403, reason="auth_group_not_allowed")
        for status in (400, 401, 429, 500, 503):
            self.assertFalse(self.check(http_counts={f"/v1/messages:http_{status}":1},
                                        rejections=[row], reviewed="auth_group_not_allowed"))
        self.assertFalse(self.check(http_counts={"/v1/messages:http_403":2}, rejections=[row],
                                    reviewed="auth_group_not_allowed"))
        self.assertFalse(self.check(http_counts={"/v1/messages:http_403":2}, rejections=[row,row],
                                    reviewed="auth_group_not_allowed"))

    def test_malformed_policy_evidence_fails_closed(self):
        row = dict(request_id="", path="/v1/messages", status=403, reason="auth_group_not_allowed")
        self.assertFalse(self.check(http_counts={"/v1/messages:http_403":1}, rejections=[row], reviewed="auth_group_not_allowed"))
        self.assertFalse(self.check(http_counts={"/v1/messages:http_403":-1}))


if __name__ == '__main__':
    unittest.main()
