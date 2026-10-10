import { join } from 'node:path';

const root = join(process.cwd(), 'tmp', 'central-demo');
console.log(JSON.stringify({
  state_path: join(root, 'state.db'),
  server: { address: '127.0.0.1:8788' },
  auth: {
    mode: 'oidc', public_url: 'http://127.0.0.1:8788',
    oidc: { issuer: 'http://127.0.0.1:18080/realms/ohyeah', client_id: 'ohyeah' },
    principals: [
      { subject: '8c32df15-6341-45f3-8f4c-c724b14c7101', name: 'Administrator', admin: true },
      { subject: '8c32df15-6341-45f3-8f4c-c724b14c7102', name: 'Reader', projects: ['demo'] },
      { subject: 'service:demo-node', name: 'Remote collector', projects: ['demo'], upload_sources: ['demo/remote/node'] },
    ],
  },
  meilisearch: { mode: 'managed', url: 'http://127.0.0.1:7702', directory: join(root, 'meilisearch'), index: 'central_demo', embedding: { enabled: false } },
  projects: {
    demo: { workspace: '/logical/demo', types: { remote: { collector: { mode: 'upload', revision: 1 }, mounts: { node: { root: '/logical/documents', revision: 1, writer: 'service:demo-node' } } } } },
  },
}, null, 2));
