export interface PriceOptions {
  currency: Currency;
  rounding: Rounding;
}

export type Currency = 'EUR' | 'USD';

export enum Rounding {
  Up,
  Down,
}

export function formatPrice(cents: number, opts: PriceOptions): string {
  return `${(cents / 100).toFixed(2)} ${opts.currency}`;
}

export class PriceFormatter {
  constructor(private readonly opts: PriceOptions) {}

  format(cents: number): string {
    return formatPrice(cents, this.opts);
  }
}
