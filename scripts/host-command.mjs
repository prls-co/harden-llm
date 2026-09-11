import { spawnSync } from 'node:child_process';

export function command(bin, args, options = {}) {
  const result = spawnSync(bin, args, { encoding: 'utf8', timeout: 900_000, maxBuffer: 8 * 1024 * 1024, ...options });
  if (result.status !== 0 || result.error) {
    // Subprocess output can contain connection strings, credentials or input.
    throw new Error(`${bin} ${args[0] ?? ''} failed (exit ${result.status ?? result.error?.code}); inspect the scoped service locally`);
  }
  return (result.stdout ?? '').trim();
}
