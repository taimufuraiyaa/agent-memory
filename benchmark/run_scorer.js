const { spawn } = require('child_process');
const path = require('path');

const runDir = path.resolve(__dirname, 'results/continuation-full-10000');
const dbPath = path.resolve(runDir, 'benchmark.db');
const scoreScript = path.resolve(__dirname, 'score.py');

console.log('Running scorer...');
const child = spawn('python3', [
  scoreScript,
  '--run-dir', runDir,
  '--db', dbPath,
  '--ingest',
  '--format', 'raw'
], {
  cwd: path.resolve(__dirname, '..'),
  stdio: 'inherit'
});

child.on('close', (code) => {
  console.log(`Process exited with code ${code}`);
  process.exit(code);
});
