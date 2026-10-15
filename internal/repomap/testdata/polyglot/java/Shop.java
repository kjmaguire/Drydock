package shop;

public class Shop {
    private Inventory inventory = new Inventory();

    public int available(String sku) {
        StockUnit unit = StockUnit.EACH;
        return inventory.countStock(sku);
    }
}
