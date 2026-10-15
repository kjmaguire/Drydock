pub struct Engine {
    speed: u32,
}

impl Engine {
    pub fn start_engine(&self) -> u32 {
        self.speed
    }
}

pub trait Runner {
    fn run(&self);
}

pub fn build_engine() -> Engine {
    Engine { speed: 1 }
}
