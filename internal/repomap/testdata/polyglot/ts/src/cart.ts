import { formatPrice, PriceFormatter, type PriceOptions, Rounding } from './format.ts';

const defaults: PriceOptions = { currency: 'EUR', rounding: Rounding.Up };

export function cartTotal(items: number[]): string {
  const sum = items.reduce((a, b) => a + b, 0);
  return formatPrice(sum, defaults);
}

export class CartView {
  render(items: number[]): string {
    const f = new PriceFormatter(defaults);
    return items.map((i) => f.format(i)).join(', ');
  }
}
